package engine

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
)

const (
	recallCandidateK    = 60
	recallSiblingBudget = 35
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

	eventDate := time.Now().UTC()
	if input.EventDate != nil {
		eventDate = input.EventDate.UTC()
	}
	eventDateStr := eventDate.Format("2006-01-02")

	decomposition, err := e.ai.DecomposeRecall(ctx, DecomposeRecallRequest{
		Content:   input.Query,
		EventDate: eventDateStr,
	})
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

	// Cosine rerank restores semantic ordering after sibling expansion mixes in
	// source-grouped facts that may not be in similarity order.
	candidates = cosineRerank(candidates, embeddings)

	// Drop near-duplicate candidates whose normalized text matches an
	// already-kept candidate. This compensates for ingest-time dedup gaps
	// (the LLM evaluator occasionally lets identical facts through, especially
	// when a USER message and the AGENT echo produce the same statement).
	beforeDedup := len(candidates)
	candidates = dedupByText(candidates)
	if dropped := beforeDedup - len(candidates); dropped > 0 {
		log.Printf("recall dedup-by-text dropped=%d remaining=%d", dropped, len(candidates))
	}

	// Temporal rerank: demote facts whose referenced_at is after the query event_date
	// (future facts). Timeless facts and past/present facts retain embedding order.
	candidates, eligibleCount, futureCount := dateRerank(candidates, eventDate)
	log.Printf("recall date-reranked event_date=%s eligible=%d future=%d", eventDateStr, eligibleCount, futureCount)

	selected, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
		Query:      input.Query,
		EventDate:  eventDateStr,
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
			// Eligible = no referenced_at (timeless) or referenced_at not in the future.
			eligible := f.ReferencedAt == nil || !f.ReferencedAt.After(eventDate)
			text := f.Text
			if len(text) > 120 {
				text = text[:120] + "…"
			}
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
				InWindow:     eligible,
				Selected:     selectedSet[f.ID],
			})
		}

		dbg = &models.RecallDebug{
			Query:            input.Query,
			Phrases:          phrases,
			EventDate:        eventDateStr,
			RetrievedCount:   retrievedCount,
			ExpandedCount:    expandedCount,
			InWindowCount:    eligibleCount,
			OutOfWindowCount: futureCount,
			DateWindowDays:   0,
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
// Round-robin expansion: pass r adds the r-th sibling from each ranked source before
// any source gets its (r+1)-th. This guarantees that every seed source contributes at
// least one sibling before the budget is consumed by a single source's long sibling
// list. Without this, a high-rank source with many siblings can monopolize the budget
// and starve lower-rank sources whose siblings carry the actual answer.
//
// Superseded siblings are included so that historical context (e.g. "original job
// title") is reachable.
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

	maxLen := 0
	for _, sid := range rankedSourceIDs {
		if n := len(bySource[sid]); n > maxLen {
			maxLen = n
		}
	}

	added := 0
	for round := 0; round < maxLen && added < budget; round++ {
		for _, sid := range rankedSourceIDs {
			if added >= budget {
				break
			}
			if round >= len(bySource[sid]) {
				continue
			}
			sib := bySource[sid][round]
			if _, dup := existing[sib.ID]; dup {
				continue
			}
			seeds = append(seeds, sib)
			existing[sib.ID] = struct{}{}
			added++
		}
	}
	if added > 0 {
		log.Printf("recall sibling expansion: added=%d sources=%d total=%d", added, len(rankedSourceIDs), len(seeds))
	}
	return seeds, nil
}

// dateRerank partitions candidates using the recall event_date as an as-of boundary.
// No candidates are dropped. Three groups (relative order preserved within each):
//
//   - No referenced_at: timeless facts — eligible, position preserved.
//   - referenced_at <= eventDate: past/present facts — eligible, position preserved.
//   - referenced_at > eventDate: facts about future events — demoted to end.
//
// This replaces the old ±windowDays proximity boost, which incorrectly promoted
// recently-dated facts over historically-dated ones for past-event queries.
func dateRerank(candidates []models.Fact, eventDate time.Time) ([]models.Fact, int, int) {
	eligible := make([]models.Fact, 0, len(candidates))
	future := make([]models.Fact, 0)
	for _, f := range candidates {
		if f.ReferencedAt == nil || !f.ReferencedAt.After(eventDate) {
			eligible = append(eligible, f)
		} else {
			future = append(future, f)
		}
	}
	return append(eligible, future...), len(eligible), len(future)
}

// dedupByText removes candidates whose normalized text equals a candidate
// earlier in the slice. The first occurrence is kept (which, after cosineRerank,
// is the highest-scoring representative of each duplicate cluster).
//
// Comparison is purely textual after normalizeFactText, so this only collapses
// statements that carry identical information. Paraphrases with non-trivial
// wording differences are preserved — semantic dedup is the SelectFacts LLM's job.
func dedupByText(candidates []models.Fact) []models.Fact {
	if len(candidates) <= 1 {
		return candidates
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]models.Fact, 0, len(candidates))
	for _, f := range candidates {
		key := normalizeFactText(f.Text)
		if key == "" {
			out = append(out, f)
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

// cosineRerank sorts candidates by their maximum cosine similarity against any of the
// query phrase embeddings, using the stored fact embeddings. Facts with no embedding
// score 0 and fall to the end. Relative order among equal-scoring facts is preserved
// (stable sort).
//
// Applied after sibling expansion so that siblings injected in source order are
// correctly interleaved with semantically-retrieved facts before SelectFacts sees them.
func cosineRerank(candidates []models.Fact, queryEmbeddings [][]float64) []models.Fact {
	if len(candidates) == 0 || len(queryEmbeddings) == 0 {
		return candidates
	}
	type scored struct {
		fact  models.Fact
		score float64
	}
	ss := make([]scored, len(candidates))
	for i, f := range candidates {
		ss[i] = scored{fact: f, score: maxCosine(f.Embedding, queryEmbeddings)}
	}
	sort.SliceStable(ss, func(i, j int) bool {
		return ss[i].score > ss[j].score
	})
	out := make([]models.Fact, len(ss))
	for i, s := range ss {
		out[i] = s.fact
	}
	return out
}

// maxCosine returns the maximum cosine similarity between vec and any of the query vectors.
func maxCosine(vec []float64, queries [][]float64) float64 {
	if len(vec) == 0 {
		return 0
	}
	normVec := l2Norm(vec)
	if normVec == 0 {
		return 0
	}
	best := 0.0
	for _, q := range queries {
		if len(q) == 0 {
			continue
		}
		normQ := l2Norm(q)
		if normQ == 0 {
			continue
		}
		n := len(vec)
		if len(q) < n {
			n = len(q)
		}
		dot := 0.0
		for k := 0; k < n; k++ {
			dot += vec[k] * q[k]
		}
		if sim := dot / (normVec * normQ); sim > best {
			best = sim
		}
	}
	return best
}

// l2Norm returns the Euclidean (L2) norm of v.
func l2Norm(v []float64) float64 {
	sum := 0.0
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
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
