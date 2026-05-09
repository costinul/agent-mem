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
	recallLightCandidateK    = 200
	recallLightSiblingBudget = 35
)

// RecallLight answers a free-text query using a single embedding pass followed by
// a cheap Flash Lite SelectFacts call. No query decomposition is performed.
func (e *MemoryEngine) RecallLight(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
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

	candidates, retrievalScores, err := e.retrieveFactsWithLimit(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings, recallLightCandidateK, true, &eventDate)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall-light retrieved=%d", len(candidates))
	retrievedCount := len(candidates)

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallLightSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall-light expanded=%d", len(candidates))
	expandedCount := len(candidates)

	candidates = cosineRerank(candidates, embeddings)

	beforeDedup := len(candidates)
	candidates = dedupByText(candidates)
	if dropped := beforeDedup - len(candidates); dropped > 0 {
		log.Printf("recall-light dedup dropped=%d remaining=%d", dropped, len(candidates))
	}

	candidates, eligibleCount, futureCount := dateRerank(candidates, eventDate)
	log.Printf("recall-light date-reranked eligible=%d future=%d", eligibleCount, futureCount)

	candidates = projectSupersessionAsOf(candidates, eventDate)

	selected, err := e.ai.SelectFactsLight(ctx, SelectFactsRequest{
		Query:      input.Query,
		EventDate:  eventDateStr,
		Candidates: candidates,
	})
	if err != nil {
		return models.RecallOutput{}, fmt.Errorf("select facts light: %w", err)
	}
	log.Printf("recall-light selected=%d ids=%v", len(selected), recallIDs(selected))

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
			Historical:   f.SupersededAt != nil,
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
