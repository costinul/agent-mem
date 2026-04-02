package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	models "agentmem/internal/models"
)

func (e *MemoryEngine) Recall(ctx context.Context, input models.RecallInput) (models.MemoryOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" {
		return models.MemoryOutput{}, errors.New("account_id is required")
	}
	if strings.TrimSpace(input.Query) == "" {
		return models.MemoryOutput{}, errors.New("query is required")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	embeddings, err := e.ai.Embed(ctx, []string{input.Query})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("embed recall query: %w", err)
	}

	retrieved, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, limit)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	return e.buildOutput(ctx, models.MemoryInput{IncludeSources: true}, retrieved)
}
