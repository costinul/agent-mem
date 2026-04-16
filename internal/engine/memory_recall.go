package engine

import (
	"context"
	"fmt"
	"strings"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
)

func (e *MemoryEngine) Recall(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" {
		return models.RecallOutput{}, errs.NewValidation("account_id is required")
	}
	if strings.TrimSpace(input.Query) == "" {
		return models.RecallOutput{}, errs.NewValidation("query is required")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	decomposition, err := e.ai.DecomposeRecall(ctx, input.Query)
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("decompose recall query: %w", err)
	}

	phrases := make([]string, 0, len(decomposition.Queries))
	for _, q := range decomposition.Queries {
		phrases = append(phrases, q.Text)
	}
	if len(phrases) == 0 {
		phrases = []string{input.Query}
	}

	embeddings, err := e.ai.Embed(ctx, phrases)
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("embed recall search phrases: %w", err)
	}

	queries := make([]searchQuery, len(phrases))
	for i := range phrases {
		queries[i] = searchQuery{Text: phrases[i], Embedding: embeddings[i]}
	}

	retrieved, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, queries, limit)
	if err != nil {
		return models.RecallOutput{}, err
	}

	return e.buildRecallOutput(ctx, input, retrieved)
}

func (e *MemoryEngine) ListThreadMessages(ctx context.Context, threadID string, limit int) ([]models.ConversationMessage, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errs.NewValidation("thread_id is required")
	}
	if limit <= 0 {
		limit = 20
	}

	sources, err := e.repo.ListConversationSourcesByThreadID(ctx, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("list thread messages: %w", err)
	}

	messages := make([]models.ConversationMessage, 0, len(sources))
	for _, source := range sources {
		content := ""
		if source.Content != nil {
			content = *source.Content
		}
		messages = append(messages, models.ConversationMessage{
			SourceID:  source.ID,
			EventID:   source.EventID,
			ThreadID:  threadID,
			Kind:      source.Kind,
			Content:   content,
			CreatedAt: source.CreatedAt,
		})
	}
	return messages, nil
}
