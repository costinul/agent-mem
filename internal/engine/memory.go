package engine

import (
	"agentmem/internal/database"

	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	client *bwaiclient.BWAIClient
	db     *database.DB
}

func NewMemoryEngine(client *bwaiclient.BWAIClient, db *database.DB) *MemoryEngine {
	return &MemoryEngine{
		client: client,
		db:     db,
	}
}
