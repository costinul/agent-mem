package engine

import (
	"agentmem/internal/repository/memoryrepo"

	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	client *bwaiclient.BWAIClient
	repo   memoryrepo.Repository
}

func NewMemoryEngine(client *bwaiclient.BWAIClient, repo memoryrepo.Repository) *MemoryEngine {
	return &MemoryEngine{
		client: client,
		repo:   repo,
	}
}
