package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port     string
	GinMode  string
	LogLevel string
	Database DatabaseConfig
	AI       AIConfig
	Admin    AdminConfig
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
	ModelDecomposeQueries string
	ModelDecomposeRecall  string
	EmbeddingModel        string
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
			ModelDecomposeQueries: os.Getenv("AI_MODEL_DECOMPOSE_QUERIES"),
			ModelDecomposeRecall:  os.Getenv("AI_MODEL_DECOMPOSE_RECALL"),
			EmbeddingModel:        os.Getenv("AI_EMBEDDING_MODEL"),
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
