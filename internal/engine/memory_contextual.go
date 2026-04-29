package engine

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

// ProcessContextual runs the full write pipeline for a conversational (thread-scoped) memory input:
// it inserts an event, decomposes sources into facts, retrieves similar existing facts,
// evaluates what to store/update/evolve, and persists the result.
func (e *MemoryEngine) ProcessContextual(ctx context.Context, input models.MemoryInput) (models.WriteOutput, error) {
	if err := validateContextualInput(input); err != nil {
		return models.WriteOutput{}, err
	}

	// Enforce monotonic event_date ordering within the thread to prevent the butterfly effect:
	// inserting a past event retroactively invalidates the supersession chain for all later facts.
	if err := e.enforceEventDateMonotonicity(ctx, input.ThreadID, input.Inputs); err != nil {
		return models.WriteOutput{}, err
	}

	tracker := &CallTracker{}
	ctx = withTracker(ctx, tracker)

	log.Printf("contextual pipeline start account=%s agent=%s thread=%s inputs=%d", input.AccountID, input.AgentID, input.ThreadID, len(input.Inputs))
	threadID := input.ThreadID
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		ThreadID:  &threadID,
	})
	if err != nil {
		return models.WriteOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, input.ThreadID, input.Inputs, true)
	if err != nil {
		return models.WriteOutput{}, err
	}

	embeddings, err := e.buildSearchEmbeddings(ctx, decompositions)
	if err != nil {
		return models.WriteOutput{}, err
	}

	retrieved, err := e.retrieveFacts(ctx, input.AccountID, input.AgentID, input.ThreadID, embeddings)
	if err != nil {
		return models.WriteOutput{}, err
	}

	evalInput := flattenExtractedFacts(decompositions)
	evalResult, err := e.ai.Evaluate(ctx, EvaluateRequest{
		NewFacts:       evalInput,
		RetrievedFacts: retrieved,
	})
	if err != nil {
		return models.WriteOutput{}, fmt.Errorf("evaluate facts: %w", err)
	}

	storedFacts, err := e.applyEvaluateResult(ctx, input, storedSources, retrieved, evalResult)
	if err != nil {
		return models.WriteOutput{}, err
	}

	log.Printf("contextual pipeline completed event=%s stored_facts=%d", event.ID, len(storedFacts))
	return models.WriteOutput{Duration: tracker.Stats()}, nil
}

// validateContextualInput ensures all required fields for a contextual memory write are present.
func validateContextualInput(input models.MemoryInput) error {
	if strings.TrimSpace(input.AccountID) == "" {
		return errs.NewValidation("account_id is required")
	}
	if strings.TrimSpace(input.ThreadID) == "" {
		return errs.NewValidation("thread_id is required")
	}
	if strings.TrimSpace(input.AgentID) == "" {
		return errs.NewValidation("thread agent is required")
	}
	if len(input.Inputs) == 0 {
		return errs.NewValidation("inputs are required")
	}
	for idx, item := range input.Inputs {
		if strings.TrimSpace(item.Content) == "" {
			return errs.NewValidation("inputs[%d].content is required", idx)
		}
		if _, ok := models.SourceTrustHierarchy[item.Kind]; !ok {
			return errs.NewValidation("inputs[%d].kind is invalid", idx)
		}
	}
	return nil
}

// buildSearchEmbeddings produces embeddings for all extracted facts and search queries
// across the given decompositions, used to find similar existing facts.
func (e *MemoryEngine) buildSearchEmbeddings(ctx context.Context, decompositions []models.Decomposition) ([][]float64, error) {
	texts := make([]string, 0)
	for _, decomposition := range decompositions {
		for _, fact := range decomposition.Facts {
			texts = append(texts, fact.Text)
		}
		for _, query := range decomposition.Queries {
			texts = append(texts, query.Text)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed search queries: %w", err)
	}
	return embeddings, nil
}

// retrieveFacts searches the thread, agent, and account scopes for facts similar to the given embeddings,
// returning up to 10 top-scored results deduplicated across scopes.
func (e *MemoryEngine) retrieveFacts(ctx context.Context, accountID, agentID, threadID string, embeddings [][]float64) ([]models.Fact, error) {
	return e.retrieveFactsWithLimit(ctx, accountID, agentID, threadID, embeddings, 10)
}

// retrieveFactsWithLimit is the same as retrieveFacts but with a configurable result cap.
// It queries three scopes in order (thread → agent → account) per phrase and deduplicates
// by highest score.
//
// Per-phrase budget: each phrase is guaranteed a slice of the final budget (perPhraseLimit
// = limit / nPhrases). Without this, a single phrase whose embedding produces a tight
// cluster of high-similarity hits can squeeze every other phrase out of the top-`limit`
// after global sort. That breaks plan/intent recall, where a verb-form phrase
// ("Melanie thinking about going camping") may rank lower than a noun-form phrase
// ("Melanie's camping plans") yet produce the only candidate that actually answers
// the question.
func (e *MemoryEngine) retrieveFactsWithLimit(ctx context.Context, accountID, agentID, threadID string, embeddings [][]float64, limit int) ([]models.Fact, error) {
	if limit <= 0 {
		limit = 10
	}
	aid := ptrString(agentID)
	tid := ptrString(threadID)

	nonEmptyPhrases := 0
	for _, emb := range embeddings {
		if len(emb) > 0 {
			nonEmptyPhrases++
		}
	}
	if nonEmptyPhrases == 0 {
		return nil, nil
	}

	perPhraseLimit := limit / nonEmptyPhrases
	if perPhraseLimit < 1 {
		perPhraseLimit = 1
	}

	scored := map[string]memoryrepo.FactWithScore{}
	mergeIntoGlobal := func(fs memoryrepo.FactWithScore) {
		if fs.Fact.ID == "" {
			return
		}
		if existing, ok := scored[fs.Fact.ID]; !ok || fs.Score > existing.Score {
			scored[fs.Fact.ID] = fs
		}
	}

	for _, emb := range embeddings {
		if len(emb) == 0 {
			continue
		}

		phraseScored := map[string]memoryrepo.FactWithScore{}
		collect := func(results []memoryrepo.FactWithScore) {
			for _, r := range results {
				if r.Fact.ID == "" {
					continue
				}
				if existing, ok := phraseScored[r.Fact.ID]; !ok || r.Score > existing.Score {
					phraseScored[r.Fact.ID] = r
				}
			}
		}

		params := memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			Embedding: emb,
			Limit:     limit,
		}

		params.AgentID = aid
		params.ThreadID = tid
		threadResults, err := e.repo.SearchFactsByEmbeddingWithScores(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("search thread facts: %w", err)
		}
		collect(threadResults)

		params.ThreadID = nil
		agentResults, err := e.repo.SearchFactsByEmbeddingWithScores(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("search agent facts: %w", err)
		}
		collect(agentResults)

		params.AgentID = nil
		accountResults, err := e.repo.SearchFactsByEmbeddingWithScores(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("search account facts: %w", err)
		}
		collect(accountResults)

		phraseSorted := make([]memoryrepo.FactWithScore, 0, len(phraseScored))
		for _, fs := range phraseScored {
			phraseSorted = append(phraseSorted, fs)
		}
		sort.Slice(phraseSorted, func(i, j int) bool {
			return phraseSorted[i].Score > phraseSorted[j].Score
		})
		if len(phraseSorted) > perPhraseLimit {
			phraseSorted = phraseSorted[:perPhraseLimit]
		}
		for _, fs := range phraseSorted {
			mergeIntoGlobal(fs)
		}
	}

	sorted := make([]memoryrepo.FactWithScore, 0, len(scored))
	for _, fs := range scored {
		sorted = append(sorted, fs)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	facts := make([]models.Fact, len(sorted))
	for i, fs := range sorted {
		facts[i] = fs.Fact
	}
	return facts, nil
}

// applyEvaluateResult persists the LLM evaluation decision. The retrieved set
// (the existing in-scope facts that were passed to Evaluate) is forwarded to
// storeNewFacts as a deterministic dedup safety net against LLM evaluator misses.
func (e *MemoryEngine) applyEvaluateResult(
	ctx context.Context,
	input models.MemoryInput,
	storedSources []models.Source,
	retrieved []models.Fact,
	result models.EvaluateResult,
) ([]models.Fact, error) {
	stored, err := e.storeNewFacts(ctx, input, storedSources, retrieved, result.FactsToStore)
	if err != nil {
		return nil, err
	}
	if err := e.updateExistingFacts(ctx, result.FactsToUpdate); err != nil {
		return nil, err
	}
	evolved, err := e.evolveFacts(ctx, input, storedSources, result.FactsToEvolve)
	if err != nil {
		return nil, err
	}
	return append(stored, evolved...), nil
}

// storeNewFacts persists each new fact that does not collide with an existing
// retrieved fact or with another new fact already inserted in this batch.
// Collision is decided by normalizeFactText: identical normalized text means
// the same statement, regardless of provenance suffix or formatting noise.
func (e *MemoryEngine) storeNewFacts(ctx context.Context, input models.MemoryInput, sources []models.Source, retrieved []models.Fact, facts []models.Fact) ([]models.Fact, error) {
	if len(facts) == 0 {
		return nil, nil
	}

	seenText := make(map[string]struct{}, len(retrieved)+len(facts))
	for _, r := range retrieved {
		if key := normalizeFactText(r.Text); key != "" {
			seenText[key] = struct{}{}
		}
	}

	keepIdx := make([]int, 0, len(facts))
	for i, f := range facts {
		key := normalizeFactText(f.Text)
		if key == "" {
			keepIdx = append(keepIdx, i)
			continue
		}
		if _, dup := seenText[key]; dup {
			log.Printf("storeNewFacts: skipping duplicate fact (matches existing or earlier new fact) text=%q", f.Text)
			continue
		}
		seenText[key] = struct{}{}
		keepIdx = append(keepIdx, i)
	}
	if len(keepIdx) == 0 {
		return nil, nil
	}

	texts := make([]string, len(keepIdx))
	for i, idx := range keepIdx {
		texts[i] = facts[idx].Text
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed new facts: %w", err)
	}
	if len(embeddings) != len(keepIdx) {
		return nil, fmt.Errorf("embed new facts: expected %d embeddings, got %d", len(keepIdx), len(embeddings))
	}

	stored := make([]models.Fact, 0, len(keepIdx))
	for i, idx := range keepIdx {
		if len(embeddings[i]) == 0 {
			return nil, fmt.Errorf("embed new facts: empty embedding for fact index %d", idx)
		}
		fact := facts[idx]
		newFact := models.Fact{
			AccountID:    input.AccountID,
			AgentID:      ptrString(input.AgentID),
			ThreadID:     ptrString(input.ThreadID),
			SourceID:     selectSourceIDForExtractedFact(sources, idx),
			Kind:         fact.Kind,
			Text:         fact.Text,
			ReferencedAt: fact.ReferencedAt,
			Embedding:    embeddings[i],
		}
		inserted, err := e.repo.InsertFact(ctx, newFact)
		if err != nil {
			return nil, fmt.Errorf("insert fact: %w", err)
		}
		stored = append(stored, *inserted)
	}
	return stored, nil
}

func (e *MemoryEngine) updateExistingFacts(ctx context.Context, facts []models.Fact) error {
	if len(facts) == 0 {
		return nil
	}
	texts := make([]string, len(facts))
	for i, f := range facts {
		texts[i] = strings.TrimSpace(f.Text)
		facts[i].Text = texts[i]
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed updated facts: %w", err)
	}
	if len(embeddings) != len(facts) {
		return fmt.Errorf("embed updated facts: expected %d embeddings, got %d", len(facts), len(embeddings))
	}
	for idx := range facts {
		if len(embeddings[idx]) == 0 {
			return fmt.Errorf("embed updated facts: empty embedding for fact index %d", idx)
		}
		facts[idx].Embedding = embeddings[idx]
		if err := e.repo.UpdateFact(ctx, facts[idx]); err != nil {
			return fmt.Errorf("update fact %s: %w", facts[idx].ID, err)
		}
	}
	return nil
}

// evolveFacts supersedes each old fact with a successor created from the current event's sources.
// The first source is used as the successor's source; this is the non-obvious bit —
// the evolve decision is driven by the most recent ingest, so the new source is always the current one.
func (e *MemoryEngine) evolveFacts(ctx context.Context, input models.MemoryInput, sources []models.Source, evolutions []models.FactEvolution) ([]models.Fact, error) {
	if len(evolutions) == 0 {
		return nil, nil
	}
	texts := make([]string, len(evolutions))
	for i, ev := range evolutions {
		texts[i] = ev.NewText
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed evolved facts: %w", err)
	}
	if len(embeddings) != len(evolutions) {
		return nil, fmt.Errorf("embed evolved facts: expected %d embeddings, got %d", len(evolutions), len(embeddings))
	}
	evolved := make([]models.Fact, 0, len(evolutions))
	for idx, ev := range evolutions {
		if len(embeddings[idx]) == 0 {
			return nil, fmt.Errorf("embed evolved facts: empty embedding for fact index %d", idx)
		}
		successor := models.Fact{
			AccountID: input.AccountID,
			AgentID:   ptrString(input.AgentID),
			ThreadID:  ptrString(input.ThreadID),
			SourceID:  selectSourceIDForExtractedFact(sources, 0),
			Kind:      ev.NewKind,
			Text:      ev.NewText,
			Embedding: embeddings[idx],
		}
		if ev.NewReferencedAt != "" {
			if t, err := time.Parse("2006-01-02", ev.NewReferencedAt); err == nil {
				successor.ReferencedAt = &t
			}
		}
		inserted, err := e.repo.SupersedeFact(ctx, ev.OldFactID, successor)
		if err != nil {
			return nil, fmt.Errorf("evolve fact %s: %w", ev.OldFactID, err)
		}
		evolved = append(evolved, *inserted)
	}
	return evolved, nil
}

func printFacts(facts []models.Fact) {
	for _, fact := range facts {
		supersededBy := ""
		if fact.SupersededBy != nil {
			supersededBy = *fact.SupersededBy
		}
		fmt.Printf("fact_id=%s text=%q kind=%s suppressedby=%s\n", fact.ID, fact.Text, fact.Kind, supersededBy)
	}
}

// enforceEventDateMonotonicity rejects the batch if any input's event_date is earlier
// than the latest event_date already stored for the thread. This protects the fact
// supersession chain from retroactive edits.
func (e *MemoryEngine) enforceEventDateMonotonicity(ctx context.Context, threadID string, inputs []models.InputItem) error {
	maxExisting, err := e.repo.MaxSourceEventDateForThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("check thread event date: %w", err)
	}
	if maxExisting == nil {
		return nil
	}
	for _, item := range inputs {
		if item.EventDate != nil && item.EventDate.Before(*maxExisting) {
			return errs.NewValidation(
				"event_date %s is earlier than the thread's latest event_date %s; backdating contextual events is not allowed",
				item.EventDate.Format("2006-01-02"),
				maxExisting.Format("2006-01-02"),
			)
		}
	}
	return nil
}
