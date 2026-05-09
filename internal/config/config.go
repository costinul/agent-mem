package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port      string
	GinMode   string
	LogLevel  string
	Database  DatabaseConfig
	AI        AIConfig
	Admin     AdminConfig
	Ingestion IngestionConfig
	Recall    RecallConfig
}

type AdminConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	BaseURL            string
	SessionTTL         time.Duration
	SessionCacheTTL    time.Duration
	Enabled            bool
}

type DatabaseConfig struct {
	PostgresDSN string
}

type AIConfig struct {
	ModelDecompose        string
	ModelEvaluate         string
	ModelSelectFacts      string
	ModelSelectFactsLight string
	ModelDecomposeQueries string
	ModelDecomposeRecall  string
	EmbeddingModel        string
}

type IngestionConfig struct {
	ChunkMaxTokens     int // INGEST_CHUNK_MAX_TOKENS, default 4000
	ChunkOverlapTokens int // INGEST_CHUNK_OVERLAP_TOKENS, default 400
}

// RecallConfig controls the two-step (gap-filling) selector path used by Recall.
//
// When TwoStepEnabled is false (default), Recall sends every retrieved candidate
// to the strong selector in a single call (legacy behavior). When true, Recall
// runs in two passes:
//  1. Send the top FirstStepK candidates to the strong selector. The selector may
//     return need_more=true with a short noun phrase naming the missing piece.
//  2. If need_more, send the next SecondStepK candidates (positions FirstStepK ..
//     FirstStepK+SecondStepK) plus the missing-piece hint to the cheaper light
//     selector, which returns additional facts to merge with the round-1 selection.
//
// Total candidates considered across both rounds is at most FirstStepK + SecondStepK.
type RecallConfig struct {
	TwoStepEnabled bool // RECALL_TWO_STEP_ENABLED, default false
	FirstStepK     int  // RECALL_FIRST_STEP_K, default 50
	SecondStepK    int  // RECALL_SECOND_STEP_K, default 150
}

func Load() (*Config, error) {
	googleClientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	googleClientSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	adminEnabled := googleClientID != "" && googleClientSecret != ""

	cfg := &Config{
		Port:     getEnvOrDefault("PORT", "8080"),
		GinMode:  getEnvOrDefault("GIN_MODE", "debug"),
		LogLevel: getEnvOrDefault("LOG_LEVEL", "debug"),
		Database: DatabaseConfig{
			PostgresDSN: strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		},
		AI: AIConfig{
			ModelDecompose:        os.Getenv("AI_MODEL_DECOMPOSE"),
			ModelEvaluate:         os.Getenv("AI_MODEL_EVALUATE"),
			ModelSelectFacts:      os.Getenv("AI_MODEL_SELECT_FACTS"),
			ModelSelectFactsLight: getEnvOrDefault("AI_MODEL_SELECT_FACTS_LIGHT", "gemini-3.0-flash-lite"),
			ModelDecomposeQueries: os.Getenv("AI_MODEL_DECOMPOSE_QUERIES"),
			ModelDecomposeRecall:  os.Getenv("AI_MODEL_DECOMPOSE_RECALL"),
			EmbeddingModel:        getEnvOrDefault("AI_EMBEDDING_MODEL", "gemini-embedding-001"),
		},
		Ingestion: IngestionConfig{
			ChunkMaxTokens:     getEnvIntOrDefault("INGEST_CHUNK_MAX_TOKENS", 4000),
			ChunkOverlapTokens: getEnvIntOrDefault("INGEST_CHUNK_OVERLAP_TOKENS", 400),
		},
		Recall: RecallConfig{
			TwoStepEnabled: getEnvBoolOrDefault("RECALL_TWO_STEP_ENABLED", false),
			FirstStepK:     getEnvIntOrDefault("RECALL_FIRST_STEP_K", 50),
			SecondStepK:    getEnvIntOrDefault("RECALL_SECOND_STEP_K", 150),
		},
		Admin: AdminConfig{
			GoogleClientID:     googleClientID,
			GoogleClientSecret: googleClientSecret,
			BaseURL:            getEnvOrDefault("BASE_URL", "http://localhost:8080"),
			SessionTTL:         getEnvDurationOrDefault("SESSION_TTL", 24*time.Hour),
			SessionCacheTTL:    getEnvDurationOrDefault("SESSION_CACHE_TTL", 60*time.Second),
			Enabled:            adminEnabled,
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string

	if c.Database.PostgresDSN == "" {
		missing = append(missing, "POSTGRES_DSN")
	}

	if c.AI.ModelDecompose == "" {
		missing = append(missing, "AI_MODEL_DECOMPOSE")
	}

	if c.AI.ModelEvaluate == "" {
		missing = append(missing, "AI_MODEL_EVALUATE")
	}

	if c.AI.ModelSelectFacts == "" {
		missing = append(missing, "AI_MODEL_SELECT_FACTS")
	}

	if c.AI.ModelDecomposeQueries == "" {
		missing = append(missing, "AI_MODEL_DECOMPOSE_QUERIES")
	}

	if c.AI.ModelDecomposeRecall == "" {
		missing = append(missing, "AI_MODEL_DECOMPOSE_RECALL")
	}

	if c.AI.EmbeddingModel == "" {
		missing = append(missing, "AI_EMBEDDING_MODEL")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}

	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

func getEnvDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultValue
	}
	return d
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}

	return value
}

func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultValue
	}
	return v
}
