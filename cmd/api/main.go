package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentmem/internal/api"
	"agentmem/internal/config"
	"agentmem/internal/database"
	"agentmem/internal/memengine"

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

	log.Println("Initializing BWAI client...")
	// Note: In a production environment, you would load models and prompts from configuration
	registry, err := bwai.NewModelRegistry("models.yaml", "prompts", nil)
	if err != nil {
		log.Printf("failed to initialize model registry: %v", err)
		// For now, we'll continue with a nil registry if files are missing, 
		// but in a real app this should probably be a fatal error.
	}
	bwaiClient := bwaiclient.NewBWAIClient(registry, nil, nil)

	log.Println("Connecting to database...")
	db, err := database.Connect(cfg.Database.PostgresDSN)
	if err != nil {
		log.Printf("failed to connect to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Println("Database connection established")

	log.Println("Initializing MemoryEngine...")
	engine := memengine.NewMemoryEngine(bwaiClient, db)

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
