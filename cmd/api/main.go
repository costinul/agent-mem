package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentmem/internal/account"
	"agentmem/internal/api"
	"agentmem/internal/config"
	"agentmem/internal/database"
	"agentmem/internal/engine"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
)

func main() {
	log.Println("Starting agent-mem API...")

	log.Println("Loading configuration...")
	cfg, err := config.Load()
	if err != nil {
		log.Printf("failed to load configuration: %v", err)
		os.Exit(1)
	}
	log.Printf("Configuration loaded (port: %s, log_level: %s)", cfg.Port, cfg.LogLevel)

	log.Println("Connecting to database...")
	db, err := database.Connect(cfg.Database.PostgresDSN)
	if err != nil {
		log.Printf("failed to connect to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Println("Database connection established")

	if len(os.Args) > 1 && os.Args[1] == "create-api-key" {
		if err := runCreateAPIKeyCommand(context.Background(), db, os.Args[2:]); err != nil {
			log.Printf("create-api-key failed: %v", err)
			os.Exit(1)
		}
		return
	}

	log.Println("Initializing BWAI client...")
	// Note: In a production environment, you would load models and prompts from configuration
	registry, err := bwai.NewModelRegistry("models.yaml", "prompts", nil)
	if err != nil {
		log.Printf("failed to initialize model registry: %v", err)
		// For now, we'll continue with a nil registry if files are missing,
		// but in a real app this should probably be a fatal error.
	}
	bwaiClient := bwaiclient.NewBWAIClient(registry, nil, nil)

	log.Println("Initializing MemoryEngine...")
	engine := engine.NewMemoryEngine(bwaiClient, db)

	log.Println("Initializing API server...")
	server := api.NewServer(engine)
	go func() {
		log.Printf("Starting HTTP server on port %s", cfg.Port)
		if err := server.Start(cfg.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("failed to start API server: %v", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("failed to shutdown API server: %v", err)
	}
	log.Println("Server shutdown complete")
}

func runCreateAPIKeyCommand(ctx context.Context, db *database.DB, args []string) error {
	fs := flag.NewFlagSet("create-api-key", flag.ContinueOnError)
	accountID := fs.String("account-id", "", "existing account ID")
	accountName := fs.String("account-name", "", "new account name when account-id is not provided")
	label := fs.String("label", "", "api key label")
	expiresAtRaw := fs.String("expires-at", "", "RFC3339 timestamp for key expiration")

	if err := fs.Parse(args); err != nil {
		return err
	}

	accountSvc := account.NewService(db)
	selectedAccountID := *accountID
	if selectedAccountID == "" {
		if *accountName == "" {
			return fmt.Errorf("either --account-id or --account-name is required")
		}

		createdAccount, err := accountSvc.CreateAccount(ctx, *accountName)
		if err != nil {
			return err
		}
		selectedAccountID = createdAccount.ID
		log.Printf("Created account %s (%s)", createdAccount.Name, createdAccount.ID)
	}

	var expiresAt *time.Time
	if *expiresAtRaw != "" {
		parsed, err := time.Parse(time.RFC3339, *expiresAtRaw)
		if err != nil {
			return fmt.Errorf("invalid --expires-at value: %w", err)
		}
		utc := parsed.UTC()
		expiresAt = &utc
	}

	var labelPtr *string
	if *label != "" {
		labelPtr = label
	}

	key, plaintext, err := accountSvc.CreateAPIKey(ctx, selectedAccountID, labelPtr, expiresAt)
	if err != nil {
		return err
	}

	fmt.Printf("account_id=%s\n", key.AccountID)
	fmt.Printf("api_key_id=%s\n", key.ID)
	fmt.Printf("api_key_prefix=%s\n", key.Prefix)
	fmt.Printf("api_key=%s\n", plaintext)
	return nil
}
