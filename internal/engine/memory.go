package engine

import (
	"context"
	"fmt"

	models "agentmem/internal/models"
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

func (e *MemoryEngine) Decompose(ctx context.Context, req DecomposeRequest) (models.Decomposition, error) {
	return e.ai.Decompose(ctx, req)
}

func (e *MemoryEngine) DecomposeRecall(ctx context.Context, query string) (models.Decomposition, error) {
	return e.ai.DecomposeRecall(ctx, query)
}

func (e *MemoryEngine) SearchWithScores(ctx context.Context, query string, params memoryrepo.SearchByEmbeddingParams) ([]memoryrepo.FactWithScore, error) {
	embs, err := e.ai.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embs) == 0 {
		return nil, nil
	}
	params.Embedding = embs[0]
	return e.repo.SearchFactsByEmbeddingWithScores(ctx, params)
}
