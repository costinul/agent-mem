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
	recallZeroCandidateK    = 200
	recallZeroSiblingBudget = 35
	recallZeroDefaultLimit  = 30
)

// RecallZero answers a free-text query with no LLM calls at all: a single
// embedding pass feeds vector retrieval, the deterministic post-processing
// chain (sibling expansion, cosine rerank, text dedup, date rerank) ranks
// candidates, and the top recallZeroDefaultLimit (or input.Limit) facts are
// returned as-is. Faster and cheaper than RecallLight; trades the LLM's
// category/inference reasoning for raw embedding-driven recall.
func (e *MemoryEngine) RecallZero(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" {
		return models.RecallOutput{}, errs.NewValidation("account_id is required")
	}
	if strings.TrimSpace(input.Query) == "" {
		return models.RecallOutput{}, errs.NewValidation("query is required")
	}

	tracker := NewCallTracker(input.Debug)
	ctx = withTracker(ctx, tracker)

	eventDate := time.Now().UTC()
	if input.EventDate != nil {
		eventDate = input.EventDate.UTC()
	}
	eventDateStr := eventDate.Format("2006-01-02")

	embeddings, err := e.ai.Embed(ctx, []string{input.Query})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("embed recall query: %w", err)
	}

	candidates, retrievalScores, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, recallZeroCandidateK, true)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall-zero retrieved=%d", len(candidates))
	retrievedCount := len(candidates)

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallZeroSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall-zero expanded=%d", len(candidates))
	expandedCount := len(candidates)

	candidates = cosineRerank(candidates, embeddings)

	beforeDedup := len(candidates)
	candidates = dedupByText(candidates)
	if dropped := beforeDedup - len(candidates); dropped > 0 {
		log.Printf("recall-zero dedup dropped=%d remaining=%d", dropped, len(candidates))
	}

	candidates, eligibleCount, futureCount := dateRerank(candidates, eventDate)
	log.Printf("recall-zero date-reranked eligible=%d future=%d", eligibleCount, futureCount)

	candidates = projectSupersessionAsOf(candidates, eventDate)

	limit := recallZeroDefaultLimit
	if input.Limit > 0 {
		limit = input.Limit
	}
	selected := candidates
	if len(selected) > limit {
		selected = selected[:limit]
	}
	log.Printf("recall-zero selected=%d ids=%v", len(selected), recallIDs(selected))

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
			eligible := f.ReferencedAt == nil || !f.ReferencedAt.After(eventDate)
			text := f.Text
			var factEventDate string
			if f.EventDate != nil {
				factEventDate = f.EventDate.Format("2006-01-02")
			}
			debugCandidates = append(debugCandidates, models.DebugCandidate{
				ID:           f.ID,
				Text:         text,
				SourceID:     f.SourceID,
				Kind:         f.Kind,
				EventDate:    factEventDate,
				ReferencedAt: f.ReferencedAt,
				Score:        retrievalScores[f.ID],
				InWindow:     eligible,
				Selected:     selectedSet[f.ID],
			})
		}
		dbg = &models.RecallDebug{
			Query:            input.Query,
			Phrases:          []string{input.Query},
			EventDate:        eventDateStr,
			RetrievedCount:   retrievedCount,
			ExpandedCount:    expandedCount,
			InWindowCount:    eligibleCount,
			OutOfWindowCount: futureCount,
			Candidates:       debugCandidates,
			SelectedIDs:      selectedIDs,
		}
	}

	out, err := e.buildRecallOutput(ctx, input, selected, dbg)
	if err != nil {
		return models.RecallOutput{}, err
	}
	out.Duration = tracker.Stats()
	out.Usage = tracker.Usage()
	return out, nil
}
