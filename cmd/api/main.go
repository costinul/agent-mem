package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentmem/internal/account"
	"agentmem/internal/admin"
	agentsvc "agentmem/internal/agent"
	"agentmem/internal/api"
	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/database"
	"agentmem/internal/engine"
	"agentmem/internal/repository/accountrepo"
	"agentmem/internal/repository/agentrepo"
	"agentmem/internal/repository/memoryrepo"
	"agentmem/internal/repository/userrepo"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
)

// @title Agent Memory API
// @version 1.0
// @description This is the API for the Agent Memory service.
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization
func main() {
	log.Println("Starting agent-mem API...")

	log.Println("Loading configuration...")
	cfg, err := config.Load()
	if err != nil {
		log.Printf("failed to load configuration: %v", err)
		os.Exit(1)
	}
	configureLogger(cfg.LogLevel)
	log.Printf("Configuration loaded (port: %s, log_level: %s)", cfg.Port, cfg.LogLevel)

	log.Println("Connecting to database...")
	db, err := database.Connect(cfg.Database.PostgresDSN)
	if err != nil {
		log.Printf("failed to connect to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Println("Database connection established")

	accountRepo := accountrepo.NewPostgres(db)
	accountSvc := account.NewService(accountRepo)
	agentRepo := agentrepo.NewPostgres(db)
	agentService := agentsvc.NewService(agentRepo)
	memoryRepo := memoryrepo.NewPostgres(db)

	if len(os.Args) > 1 && os.Args[1] == "create-api-key" {
		if err := runCreateAPIKeyCommand(context.Background(), accountSvc, os.Args[2:]); err != nil {
			log.Printf("create-api-key failed: %v", err)
			os.Exit(1)
		}
		return
	}

	log.Println("Initializing BWAI client...")
	registry, err := bwai.NewModelRegistry("models.json", "prompts", &envSecretStorage{})
	if err != nil {
		log.Printf("failed to initialize model registry: %v", err)
		os.Exit(1)
	}
	bwaiClient := bwaiclient.NewBWAIClient(registry, nil, nil)

	log.Println("Initializing MemoryEngine...")
	engine := engine.NewMemoryEngine(bwaiClient, memoryRepo, cfg.AI.SchemaModel, cfg.AI.EmbeddingModel)

	var adminDeps *api.AdminDeps
	if cfg.Admin.Enabled {
		log.Println("Initializing admin UI with Google OAuth...")
		userRepo := userrepo.NewPostgres(db)
		pgSessions := auth.NewPgSessionStore(db)
		sessionStore := auth.NewCachedSessionStore(pgSessions, cfg.Admin.SessionCacheTTL, 1000)
		googleAuth := auth.NewGoogleAuth(
			cfg.Admin.GoogleClientID,
			cfg.Admin.GoogleClientSecret,
			cfg.Admin.BaseURL,
			userRepo,
			sessionStore,
			cfg.Admin.SessionTTL,
		)
		adminHandler := admin.NewHandler(accountRepo, agentRepo, memoryRepo, userRepo, engine)
		adminDeps = &api.AdminDeps{
			GoogleAuth:   googleAuth,
			SessionStore: sessionStore,
			UserRepo:     userRepo,
			AdminHandler: adminHandler,
		}
	}

	log.Println("Initializing API server...")
	server := api.NewServer(engine, accountSvc, agentService, adminDeps)
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

func configureLogger(level string) {
	logLevel := parseLogLevel(level)
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(handler, logLevel).Writer())
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// envSecretStorage resolves secrets from environment variables.
// The secretName is treated as the env var name (e.g. "OPENAI_API_KEY").
type envSecretStorage struct{}

func (s *envSecretStorage) GetSecret(_ context.Context, name string) ([]byte, error) {
	val := os.Getenv(name)
	if val == "" {
		return nil, fmt.Errorf("env var %s is not set", name)
	}
	return []byte(val), nil
}

func (s *envSecretStorage) SaveSecret(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("not supported")
}

func runCreateAPIKeyCommand(ctx context.Context, accountSvc *account.Service, args []string) error {
	fs := flag.NewFlagSet("create-api-key", flag.ContinueOnError)
	accountID := fs.String("account-id", "", "existing account ID")
	accountName := fs.String("account-name", "", "new account name when account-id is not provided")
	label := fs.String("label", "", "api key label")
	expiresAtRaw := fs.String("expires-at", "", "RFC3339 timestamp for key expiration")

	if err := fs.Parse(args); err != nil {
		return err
	}

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
