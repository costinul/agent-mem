package engine

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
)

const (
	recallCandidateK    = 25
	recallSiblingBudget = 15
)

// Recall answers a free-text query by decomposing it into search phrases, retrieving
// candidate facts across all scopes, then asking the LLM to select the most relevant ones.
func (e *MemoryEngine) Recall(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" {
		return models.RecallOutput{}, errs.NewValidation("account_id is required")
	}
	if strings.TrimSpace(input.Query) == "" {
		return models.RecallOutput{}, errs.NewValidation("query is required")
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

	candidates, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, recallCandidateK)
	if err != nil {
		return models.RecallOutput{}, err
	}

	printFacts(candidates)

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}

	fmt.Println("after expand by source")
	printFacts(candidates)

	selected, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
		Query:      input.Query,
		Candidates: candidates,
	})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("select facts: %w", err)
	}

	if input.Limit > 0 && len(selected) > input.Limit {
		selected = selected[:input.Limit]
	}

	return e.buildRecallOutput(ctx, input, selected)
}

// expandBySource augments the candidate set with facts that share a source_id with any
// seed fact. Recall queries often embed against a thought that decompose split into
// multiple atomic facts; pulling in siblings re-glues that context.
// New facts are appended up to budget; the existing order of seeds is preserved.
func (e *MemoryEngine) expandBySource(ctx context.Context, accountID string, seeds []models.Fact, budget int) ([]models.Fact, error) {
	if len(seeds) == 0 || budget <= 0 {
		return seeds, nil
	}

	existing := make(map[string]struct{}, len(seeds))
	sourceSet := make(map[string]struct{}, len(seeds))
	for _, f := range seeds {
		existing[f.ID] = struct{}{}
		if f.SourceID != "" {
			sourceSet[f.SourceID] = struct{}{}
		}
	}
	if len(sourceSet) == 0 {
		return seeds, nil
	}

	sourceIDs := make([]string, 0, len(sourceSet))
	for id := range sourceSet {
		sourceIDs = append(sourceIDs, id)
	}

	siblings, err := e.repo.ListFactsBySourceIDs(ctx, accountID, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list sibling facts: %w", err)
	}

	added := 0
	for _, s := range siblings {
		if _, dup := existing[s.ID]; dup {
			continue
		}
		seeds = append(seeds, s)
		existing[s.ID] = struct{}{}
		added++
		if added >= budget {
			break
		}
	}
	if added > 0 {
		log.Printf("recall sibling expansion: added=%d sources=%d total=%d", added, len(sourceSet), len(seeds))
	}
	return seeds, nil
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
			Author:    source.Author,
			Content:   content,
			CreatedAt: source.CreatedAt,
		})
	}
	return messages, nil
}
