package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port     string
	GinMode  string
	LogLevel string
	Database DatabaseConfig
	Cache    CacheConfig
}

type DatabaseConfig struct {
	PostgresDSN string
}

type CacheConfig struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:     getEnvOrDefault("PORT", "8080"),
		GinMode:  getEnvOrDefault("GIN_MODE", "debug"),
		LogLevel: getEnvOrDefault("LOG_LEVEL", "debug"),
		Database: DatabaseConfig{
			PostgresDSN: strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		},
		Cache: CacheConfig{
			RedisAddr:     strings.TrimSpace(os.Getenv("REDIS_ADDR")),
			RedisPassword: os.Getenv("REDIS_PASSWORD"),
			RedisDB:       getEnvIntOrDefault("REDIS_DB", 0),
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

	if c.Cache.RedisAddr == "" {
		missing = append(missing, "REDIS_ADDR")
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
