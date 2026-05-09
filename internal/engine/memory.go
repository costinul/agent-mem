package engine

import (
	"context"
	"fmt"

	"agentmem/internal/config"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"

	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	repo      memoryrepo.Repository
	ai        *LLMAdapter
	ingestion config.IngestionConfig
	recall    config.RecallConfig
}

// DefaultIngestion returns the default ingestion configuration (used in tests).
func DefaultIngestion() config.IngestionConfig {
	return config.IngestionConfig{ChunkMaxTokens: 4000, ChunkOverlapTokens: 400}
}

// DefaultRecall returns the default recall configuration (used in tests).
// Two-step is OFF by default so legacy behavior is preserved.
func DefaultRecall() config.RecallConfig {
	return config.RecallConfig{TwoStepEnabled: false, FirstStepK: 50, SecondStepK: 150}
}

func NewMemoryEngine(client *bwaiclient.BWAIClient, repo memoryrepo.Repository, llmModels LLMModels, embeddingModel string, ingestion config.IngestionConfig, recall config.RecallConfig, reg *trackerRegistry) *MemoryEngine {
	return &MemoryEngine{
		repo:      &repoWrapper{inner: repo},
		ai:        NewLLMAdapter(client, llmModels, embeddingModel, reg),
		ingestion: ingestion,
		recall:    recall,
	}
}


func (e *MemoryEngine) Decompose(ctx context.Context, req DecomposeRequest) (models.Decomposition, error) {
	return e.ai.Decompose(ctx, req)
}

func (e *MemoryEngine) DecomposeQueries(ctx context.Context, req DecomposeRequest) ([]models.ExtractedQuery, error) {
	return e.ai.DecomposeQueries(ctx, req)
}

func (e *MemoryEngine) DecomposeRecall(ctx context.Context, req DecomposeRecallRequest) (models.Decomposition, error) {
	return e.ai.DecomposeRecall(ctx, req)
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
