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
	log.Printf("recall q=%q phrases=%v", input.Query, phrases)

	embeddings, err := e.ai.Embed(ctx, phrases)
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("embed recall search phrases: %w", err)
	}

	candidates, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, recallCandidateK)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall retrieved=%d top_texts=%v", len(candidates), recallPreviews(candidates, 5))

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall expanded=%d", len(candidates))

	selected, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
		Query:      input.Query,
		Candidates: candidates,
	})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("select facts: %w", err)
	}
	log.Printf("recall selected=%d ids=%v", len(selected), recallIDs(selected))

	if input.Limit > 0 && len(selected) > input.Limit {
		selected = selected[:input.Limit]
	}

	return e.buildRecallOutput(ctx, input, selected)
}

// expandBySource augments the candidate set with facts that share a source_id with any
// seed fact. Recall queries often embed against a thought that decompose split into
// multiple atomic facts; pulling in siblings re-glues that context.
//
// Siblings are added in seed-rank order: the highest-ranked seed contributes its
// siblings first, then the next, and so on. This guarantees the most relevant hits'
// neighborhoods survive the budget cap. Superseded siblings are included so that
// historical context (e.g. "original job title") is reachable.
func (e *MemoryEngine) expandBySource(ctx context.Context, accountID string, seeds []models.Fact, budget int) ([]models.Fact, error) {
	if len(seeds) == 0 || budget <= 0 {
		return seeds, nil
	}

	existing := make(map[string]struct{}, len(seeds))
	rankedSourceIDs := make([]string, 0, len(seeds))
	seenSource := make(map[string]struct{}, len(seeds))
	for _, f := range seeds {
		existing[f.ID] = struct{}{}
		if f.SourceID == "" {
			continue
		}
		if _, ok := seenSource[f.SourceID]; ok {
			continue
		}
		seenSource[f.SourceID] = struct{}{}
		rankedSourceIDs = append(rankedSourceIDs, f.SourceID)
	}
	if len(rankedSourceIDs) == 0 {
		return seeds, nil
	}

	siblings, err := e.repo.ListFactsBySourceIDs(ctx, accountID, rankedSourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list sibling facts: %w", err)
	}

	bySource := make(map[string][]models.Fact, len(rankedSourceIDs))
	for _, s := range siblings {
		bySource[s.SourceID] = append(bySource[s.SourceID], s)
	}

	added := 0
outer:
	for _, sid := range rankedSourceIDs {
		for _, sib := range bySource[sid] {
			if _, dup := existing[sib.ID]; dup {
				continue
			}
			seeds = append(seeds, sib)
			existing[sib.ID] = struct{}{}
			added++
			if added >= budget {
				break outer
			}
		}
	}
	if added > 0 {
		log.Printf("recall sibling expansion: added=%d sources=%d total=%d", added, len(rankedSourceIDs), len(seeds))
	}
	return seeds, nil
}

func recallPreviews(facts []models.Fact, n int) []string {
	out := make([]string, 0, n)
	for i, f := range facts {
		if i >= n {
			break
		}
		text := f.Text
		if len(text) > 60 {
			text = text[:60] + "…"
		}
		out = append(out, text)
	}
	return out
}

func recallIDs(facts []models.Fact) []string {
	ids := make([]string, len(facts))
	for i, f := range facts {
		ids[i] = f.ID
	}
	return ids
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
