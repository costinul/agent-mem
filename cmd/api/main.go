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

	log.Println("Initializing API server...")
	server := api.NewServer(db)
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
