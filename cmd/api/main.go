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
	agentsvc "agentmem/internal/agent"
	"agentmem/internal/api"
	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/database"
	"agentmem/internal/engine"
	"agentmem/internal/ownerhub"
	"agentmem/internal/repository/accountrepo"
	"agentmem/internal/repository/agentrepo"
	"agentmem/internal/repository/memoryrepo"
	"agentmem/internal/repository/userrepo"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
)

// estimatedPromptOverhead is a conservative token budget for system prompt,
// metadata fields, and JSON scaffolding added by the prompt template.
const estimatedPromptOverhead = 512

// validateChunkConfig checks that the configured chunk size fits within the
// context window of the models assigned to ingestion prompts.
func validateChunkConfig(registry *bwai.ModelRegistry, cfg *config.Config) error {
	type check struct {
		prompt string
		model  string
	}
	checks := []check{
		{"decompose_content", cfg.AI.ModelDecompose},
		{"decompose_conversational", cfg.AI.ModelDecompose},
	}
	for _, c := range checks {
		pd := registry.GetPromptDefinition(c.prompt)
		if pd == nil {
			return fmt.Errorf("prompt %q not found in registry", c.prompt)
		}
		mc := registry.GetModelConfig(c.model)
		if mc == nil {
			return fmt.Errorf("model %q not found in registry (prompt=%s)", c.model, c.prompt)
		}
		required := cfg.Ingestion.ChunkMaxTokens + estimatedPromptOverhead + pd.MaxTokens
		if required > mc.ContextWindowSize {
			return fmt.Errorf(
				"chunk config too large: prompt=%s model=%s chunk_max=%d + overhead=%d + max_tokens=%d = %d > context_window=%d",
				c.prompt, c.model,
				cfg.Ingestion.ChunkMaxTokens, estimatedPromptOverhead, pd.MaxTokens,
				required, mc.ContextWindowSize,
			)
		}
	}
	return nil
}

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
	trackerReg := engine.NewTrackerRegistry()
	usageLogger := engine.NewCompositeUsageLogger(
		engine.NewTrackerUsageRecorder(trackerReg),
		// future: engine.NewBillingUsageRecorder(usageRepo) for per-account persistence
	)
	bwaiClient := bwaiclient.NewBWAIClient(registry, nil, usageLogger)

	if err := validateChunkConfig(registry, cfg); err != nil {
		log.Printf("startup validation failed: %v", err)
		os.Exit(1)
	}

	log.Println("Initializing MemoryEngine...")
	llmModels := engine.LLMModels{
		Decompose:        cfg.AI.ModelDecompose,
		Evaluate:         cfg.AI.ModelEvaluate,
		SelectFacts:      cfg.AI.ModelSelectFacts,
		SelectFactsLight: cfg.AI.ModelSelectFactsLight,
		DecomposeQueries: cfg.AI.ModelDecomposeQueries,
		DecomposeRecall:  cfg.AI.ModelDecomposeRecall,
	}
	engine := engine.NewMemoryEngine(bwaiClient, memoryRepo, llmModels, cfg.AI.EmbeddingModel, cfg.Ingestion, cfg.Recall, trackerReg)

	var ownerhubDeps *api.OwnerHubDeps
	if cfg.OwnerHub.Enabled {
		log.Println("Initializing OwnerHub UI with Google OAuth...")
		userRepo := userrepo.NewPostgres(db)
		pgSessions := auth.NewPgSessionStore(db)
		sessionStore := auth.NewCachedSessionStore(pgSessions, cfg.OwnerHub.SessionCacheTTL, 1000)
		googleAuth := auth.NewGoogleAuth(
			cfg.OwnerHub.GoogleClientID,
			cfg.OwnerHub.GoogleClientSecret,
			cfg.OwnerHub.BaseURL,
			userRepo,
			sessionStore,
			cfg.OwnerHub.SessionTTL,
		)
		hubHandler := ownerhub.NewHandler(accountRepo, accountSvc, agentRepo, memoryRepo, userRepo, engine)
		ownerhubDeps = &api.OwnerHubDeps{
			GoogleAuth:   googleAuth,
			SessionStore: sessionStore,
			UserRepo:     userRepo,
			Handler:      hubHandler,
		}
	}

	log.Println("Initializing API server...")
	server := api.NewServer(engine, accountSvc, agentService, ownerhubDeps)
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
