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
	recallCandidateK    = 100
	recallSiblingBudget = 35
)

// recallCandidateLimit returns the total number of fused-retrieval candidates the
// hybrid pass should return. In two-step mode we need enough headroom to feed the
// round-2 gap-filling selector (FirstStepK + SecondStepK); in single-step mode
// the legacy recallCandidateK budget applies.
func (e *MemoryEngine) recallCandidateLimit() int {
	if e.recall.TwoStepEnabled {
		k := e.recall.FirstStepK + e.recall.SecondStepK
		if k <= 0 {
			return recallCandidateK
		}
		return k
	}
	return recallCandidateK
}

// Recall answers a free-text query by decomposing it into search phrases, retrieving
// candidate facts across all scopes, then asking the LLM to select the most relevant ones.
func (e *MemoryEngine) Recall(ctx context.Context, input models.RecallInput) (models.RecallOutput, error) {
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
	log.Printf("recall input account=%s agent=%s thread=%s query=%q event_date=%s limit=%d include_sources=%t",
		input.AccountID, input.AgentID, input.ThreadID, input.Query, eventDateStr, input.Limit, input.IncludeSources)

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

	hybrid, err := e.retrieveFactsHybrid(ctx, input.AccountID, input.AgentID, input.ThreadID, phrases, embeddings, decomposition.Entities, e.recallCandidateLimit(), true, &eventDate)
	if err != nil {
		return models.RecallOutput{}, err
	}
	candidates := hybrid.Facts
	retrievalScores := hybrid.FusedScores
	log.Printf("recall retrieved=%d top_texts=%v entities=%v", len(candidates), recallPreviews(candidates, 5), decomposition.Entities)
	retrievedCount := len(candidates)

	candidates, err = e.expandBySource(ctx, input.AccountID, candidates, recallSiblingBudget)
	if err != nil {
		return models.RecallOutput{}, err
	}
	log.Printf("recall expanded=%d", len(candidates))
	expandedCount := len(candidates)

	// Re-sort after sibling injection so that scored candidates retain their RRF
	// ordering. Siblings (no fused score) are appended at the end; the previous
	// cosineRerank would clobber RRF order by demoting lexical/entity-only matches
	// with low dense similarity, which is the exact failure mode hybrid retrieval
	// is supposed to fix.
	candidates = rrfPlaceSiblings(candidates, hybrid.FusedScores, hybrid.DenseScores, embeddings)

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

	// Project supersession onto the recall event_date so HISTORICAL is decided as-of,
	// not by absolute timeline. A fact superseded after eventDate is still current at
	// eventDate and must not carry the HISTORICAL marker into SelectFacts.
	candidates = projectSupersessionAsOf(candidates, eventDate)

	selected, err := e.runSelector(ctx, input.Query, eventDateStr, phrases, candidates)
	if err != nil {
		return models.RecallOutput{}, err
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

		channelRank := func(reg channelRanks, id string) int {
			if r, ok := reg[id]; ok {
				return r
			}
			return -1
		}

		debugCandidates := make([]models.DebugCandidate, 0, len(candidates))
		for i, f := range candidates {
			// Eligible = no referenced_at (timeless) or referenced_at not in the future.
			eligible := f.ReferencedAt == nil || !f.ReferencedAt.After(eventDate)
			text := f.Text
			var factEventDate string
			if f.EventDate != nil {
				factEventDate = f.EventDate.Format("2006-01-02")
			}
			debugCandidates = append(debugCandidates, models.DebugCandidate{
				Index:        i + 1,
				ID:           f.ID,
				Text:         text,
				SourceID:     f.SourceID,
				Kind:         f.Kind,
				Entities:     f.Entities,
				EventDate:    factEventDate,
				ReferencedAt: f.ReferencedAt,
				Score:        retrievalScores[f.ID],
				DenseRank:    channelRank(hybrid.DenseRank, f.ID),
				LexicalRank:  channelRank(hybrid.LexicalRank, f.ID),
				EntityRank:   channelRank(hybrid.EntityRank, f.ID),
				InWindow:     eligible,
				Selected:     selectedSet[f.ID],
				Historical:   f.SupersededAt != nil,
			})
		}

		dbg = &models.RecallDebug{
			Query:            input.Query,
			Phrases:          phrases,
			Entities:         decomposition.Entities,
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

	out, err := e.buildRecallOutput(ctx, input, selected, dbg)
	if err != nil {
		return models.RecallOutput{}, err
	}
	out.Duration = tracker.Stats()
	out.Usage = tracker.Usage()
	return out, nil
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

// projectSupersessionAsOf rewrites each candidate's supersession fields so that the
// HISTORICAL marker reflects the recall event_date, not the absolute timeline.
//
//   - superseded_at <= eventDate: supersession has already happened from the recall's
//     perspective — keep both fields so the prompt renders HISTORICAL and the API
//     output reports historical=true.
//   - superseded_at > eventDate: supersession is in the future from the recall's
//     perspective — clear both fields so the fact is treated as current.
//   - superseded_at IS NULL: never superseded — already current.
//
// Returns the modified slice (candidates are passed by value as Fact structs, so
// mutating in place affects only the local copies).
func projectSupersessionAsOf(candidates []models.Fact, eventDate time.Time) []models.Fact {
	for i := range candidates {
		if candidates[i].SupersededAt == nil {
			continue
		}
		if candidates[i].SupersededAt.After(eventDate) {
			candidates[i].SupersededAt = nil
			candidates[i].SupersededBy = nil
		}
	}
	return candidates
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

// runSelector runs either the legacy single-shot selector or the two-step
// (gap-filling) selector, depending on RecallConfig.TwoStepEnabled.
//
// Two-step mode:
//  1. Send the top FirstStepK candidates to the strong selector. It returns its
//     picks plus an optional NeedMore signal naming what's missing.
//  2. If NeedMore=true and there are unseen candidates (positions FirstStepK..
//     FirstStepK+SecondStepK), send THOSE to the cheap light selector along with
//     the already-selected fact texts and the missing-piece hint. Merge the
//     additional picks with round 1, deduping by ID.
//
// The light model only sees fresh candidates, never re-evaluates round-1's picks,
// which keeps the round-2 prompt small and the cost low.
func (e *MemoryEngine) runSelector(ctx context.Context, query, eventDateStr string, phrases []string, candidates []models.Fact) ([]models.Fact, error) {
	if !e.recall.TwoStepEnabled {
		res, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
			Query:      query,
			EventDate:  eventDateStr,
			Phrases:    phrases,
			Candidates: candidates,
		})
		if err != nil {
			return nil, fmt.Errorf("select facts: %w", err)
		}
		return res.Facts, nil
	}

	firstK := e.recall.FirstStepK
	if firstK <= 0 || firstK > len(candidates) {
		firstK = len(candidates)
	}
	firstBatch := candidates[:firstK]

	res, err := e.ai.SelectFacts(ctx, SelectFactsRequest{
		Query:      query,
		EventDate:  eventDateStr,
		Phrases:    phrases,
		Candidates: firstBatch,
	})
	if err != nil {
		return nil, fmt.Errorf("select facts (round 1): %w", err)
	}
	log.Printf("recall round1 selected=%d need_more=%t missing=%q", len(res.Facts), res.NeedMore, res.Missing)

	if !res.NeedMore || res.Missing == "" || firstK >= len(candidates) {
		return res.Facts, nil
	}

	end := firstK + e.recall.SecondStepK
	if end > len(candidates) {
		end = len(candidates)
	}
	secondBatch := candidates[firstK:end]
	if len(secondBatch) == 0 {
		return res.Facts, nil
	}

	alreadyTexts := make([]string, len(res.Facts))
	for i, f := range res.Facts {
		alreadyTexts[i] = f.Text
	}

	gap, err := e.ai.SelectFactsGap(ctx, SelectFactsGapRequest{
		Query:           query,
		EventDate:       eventDateStr,
		Phrases:         phrases,
		AlreadySelected: alreadyTexts,
		Missing:         res.Missing,
		Candidates:      secondBatch,
	})
	if err != nil {
		// Round-2 failure is non-fatal: better to return round-1 picks than nothing.
		log.Printf("recall round2 gap-fill failed: %v (returning round-1 selection)", err)
		return res.Facts, nil
	}
	log.Printf("recall round2 gap-fill added=%d", len(gap))

	merged := make([]models.Fact, 0, len(res.Facts)+len(gap))
	seen := make(map[string]struct{}, len(res.Facts)+len(gap))
	for _, f := range res.Facts {
		merged = append(merged, f)
		seen[f.ID] = struct{}{}
	}
	for _, f := range gap {
		if _, dup := seen[f.ID]; dup {
			continue
		}
		merged = append(merged, f)
		seen[f.ID] = struct{}{}
	}
	return merged, nil
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
