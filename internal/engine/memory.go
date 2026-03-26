package engine

import (
	"agentmem/internal/repository/memoryrepo"

	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	repo memoryrepo.Repository
	ai   *LLMAdapter
}

func NewMemoryEngine(client *bwaiclient.BWAIClient, repo memoryrepo.Repository, schemaModel, embeddingModel string) *MemoryEngine {
	return &MemoryEngine{
		repo: repo,
		ai:   NewLLMAdapter(client, schemaModel, embeddingModel),
	}
}
