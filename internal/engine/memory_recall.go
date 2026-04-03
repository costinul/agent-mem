package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	models "agentmem/internal/models"
)

func (e *MemoryEngine) Recall(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" {
		return models.RecallOutput{}, errors.New("account_id is required")
	}
	if strings.TrimSpace(input.Query) == "" {
		return models.RecallOutput{}, errors.New("query is required")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	embeddings, err := e.ai.Embed(ctx, []string{input.Query})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("embed recall query: %w", err)
	}

	retrieved, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, limit)
	if err != nil {
		return models.RecallOutput{}, err
	}

	return e.buildRecallOutput(ctx, input, retrieved)
}
