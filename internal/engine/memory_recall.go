package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
)

const (
	recallCandidateK    = 60
	recallSiblingBudget = 35
	dateWindowDays      = 3
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
	retrievedCount := len(candidates)

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall expanded=%d", len(candidates))
	expandedCount := len(candidates)

	var inWindowCount, outOfWindowCount int
	if decomposition.QueryDate != nil {
		candidates, inWindowCount, outOfWindowCount = dateRerank(candidates, *decomposition.QueryDate, dateWindowDays)
		log.Printf("recall date-reranked query_date=%s in_window=%d out_of_window=%d", decomposition.QueryDate.Format("2006-01-02"), inWindowCount, outOfWindowCount)
	}

	selected, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
		Query:      input.Query,
		QueryDate:  decomposition.QueryDate,
		Candidates: candidates,
	})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("select facts: %w", err)
	}
	log.Printf("recall selected=%d ids=%v", len(selected), recallIDs(selected))

	if input.Limit > 0 && len(selected) > input.Limit {
		selected = selected[:input.Limit]
	}

	var dbg *models.RecallDebug
	if input.Debug {
		selectedSet := make(map[string]bool, len(selected))
		for _, f := range selected {
			selectedSet[f.ID] = true
		}

		selectedIDs := make([]string, 0, len(selected))
		for _, f := range selected {
			selectedIDs = append(selectedIDs, f.ID)
		}

		debugCandidates := make([]models.DebugCandidate, 0, len(candidates))
		for _, f := range candidates {
			inWindow := true
			if decomposition.QueryDate != nil && f.ReferencedAt != nil {
				diff := f.ReferencedAt.Sub(*decomposition.QueryDate)
				if diff < 0 {
					diff = -diff
				}
				inWindow = float64(diff) <= float64(dateWindowDays)*24*float64(time.Hour)
			}
			text := f.Text
			if len(text) > 120 {
				text = text[:120] + "…"
			}
			debugCandidates = append(debugCandidates, models.DebugCandidate{
				ID:           f.ID,
				Text:         text,
				SourceID:     f.SourceID,
				Kind:         f.Kind,
				ReferencedAt: f.ReferencedAt,
				InWindow:     inWindow,
				Selected:     selectedSet[f.ID],
			})
		}

		dbg = &models.RecallDebug{
			Query:            input.Query,
			Phrases:          phrases,
			QueryDate:        decomposition.QueryDate,
			RetrievedCount:   retrievedCount,
			ExpandedCount:    expandedCount,
			InWindowCount:    inWindowCount,
			OutOfWindowCount: outOfWindowCount,
			DateWindowDays:   dateWindowDays,
			Candidates:       debugCandidates,
			SelectedIDs:      selectedIDs,
		}
	}

	return e.buildRecallOutput(ctx, input, selected, dbg)
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

// dateRerank partitions candidates into those whose referenced_at is within windowDays
// of queryDate (or have no referenced_at) and those that are clearly out-of-window.
// In-window facts are returned first (preserving original order); out-of-window facts follow.
// No candidates are dropped — a wrong query_date must not nuke recall.
// Returns the reordered slice plus in-window and out-of-window counts.
func dateRerank(candidates []models.Fact, queryDate time.Time, windowDays int) ([]models.Fact, int, int) {
	threshold := float64(windowDays) * 24 * float64(time.Hour)
	inWindow := make([]models.Fact, 0, len(candidates))
	outOfWindow := make([]models.Fact, 0)
	for _, f := range candidates {
		if f.ReferencedAt == nil {
			inWindow = append(inWindow, f)
			continue
		}
		diff := f.ReferencedAt.Sub(queryDate)
		if diff < 0 {
			diff = -diff
		}
		if float64(diff) <= threshold {
			inWindow = append(inWindow, f)
		} else {
			outOfWindow = append(outOfWindow, f)
		}
	}
	return append(inWindow, outOfWindow...), len(inWindow), len(outOfWindow)
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
