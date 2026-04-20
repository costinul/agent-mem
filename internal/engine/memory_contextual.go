package engine

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

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

	storedFacts, err := e.applyEvaluateResult(ctx, input, storedSources, evalInput, evalResult)
	if err != nil {
		return models.WriteOutput{}, err
	}

	log.Printf("contextual pipeline completed event=%s stored_facts=%d", event.ID, len(storedFacts))
	return models.WriteOutput{}, nil
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
// It queries three scopes in order (thread → agent → account) and deduplicates by highest score.
func (e *MemoryEngine) retrieveFactsWithLimit(ctx context.Context, accountID, agentID, threadID string, embeddings [][]float64, limit int) ([]models.Fact, error) {
	if limit <= 0 {
		limit = 10
	}
	aid := ptrString(agentID)
	tid := ptrString(threadID)
	scored := map[string]memoryrepo.FactWithScore{}

	collect := func(results []memoryrepo.FactWithScore) {
		for _, r := range results {
			if r.Fact.ID == "" {
				continue
			}
			if existing, ok := scored[r.Fact.ID]; !ok || r.Score > existing.Score {
				scored[r.Fact.ID] = r
			}
		}
	}

	for _, emb := range embeddings {
		if len(emb) == 0 {
			continue
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

// applyEvaluateResult persists the LLM evaluation decision: inserts new facts,
// updates existing ones in-place, and supersedes evolved facts with a new version.
func (e *MemoryEngine) applyEvaluateResult(
	ctx context.Context,
	input models.MemoryInput,
	storedSources []models.Source,
	newFacts []models.ExtractedFact,
	result models.EvaluateResult,
) ([]models.Fact, error) {
	newTexts := make([]string, 0, len(result.FactsToStore))
	for _, fact := range result.FactsToStore {
		newTexts = append(newTexts, fact.Text)
	}
	embeddings, err := e.ai.Embed(ctx, newTexts)
	if err != nil {
		return nil, fmt.Errorf("embed new facts: %w", err)
	}
	if len(embeddings) != len(newTexts) {
		return nil, fmt.Errorf("embed new facts: expected %d embeddings, got %d", len(newTexts), len(embeddings))
	}

	stored := make([]models.Fact, 0, len(result.FactsToStore))
	for idx, fact := range result.FactsToStore {
		sourceID := selectSourceIDForExtractedFact(storedSources, idx)
		newFact := models.Fact{
			AccountID: input.AccountID,
			AgentID:   ptrString(input.AgentID),
			ThreadID:  ptrString(input.ThreadID),
			SourceID:  sourceID,
			Kind:      fact.Kind,
			Text:      fact.Text,
		}
		if len(embeddings[idx]) == 0 {
			return nil, fmt.Errorf("embed new facts: empty embedding for fact index %d", idx)
		}
		newFact.Embedding = embeddings[idx]
		inserted, err := e.repo.InsertFact(ctx, newFact)
		if err != nil {
			return nil, fmt.Errorf("insert contextual fact: %w", err)
		}
		stored = append(stored, *inserted)
	}

	if len(result.FactsToUpdate) > 0 {
		updateTexts := make([]string, 0, len(result.FactsToUpdate))
		for idx := range result.FactsToUpdate {
			result.FactsToUpdate[idx].Text = strings.TrimSpace(result.FactsToUpdate[idx].Text)
			updateTexts = append(updateTexts, result.FactsToUpdate[idx].Text)
		}
		updateEmbeddings, err := e.ai.Embed(ctx, updateTexts)
		if err != nil {
			return nil, fmt.Errorf("embed updated facts: %w", err)
		}
		if len(updateEmbeddings) != len(result.FactsToUpdate) {
			return nil, fmt.Errorf("embed updated facts: expected %d embeddings, got %d", len(result.FactsToUpdate), len(updateEmbeddings))
		}
		for idx := range result.FactsToUpdate {
			if len(updateEmbeddings[idx]) == 0 {
				return nil, fmt.Errorf("embed updated facts: empty embedding for fact index %d", idx)
			}
			result.FactsToUpdate[idx].Embedding = updateEmbeddings[idx]
		}
	}

	for _, fact := range result.FactsToUpdate {
		if err := e.repo.UpdateFact(ctx, fact); err != nil {
			return nil, fmt.Errorf("update contextual fact: %w", err)
		}
	}

	if len(result.FactsToEvolve) > 0 {
		evolveTexts := make([]string, 0, len(result.FactsToEvolve))
		for _, ev := range result.FactsToEvolve {
			evolveTexts = append(evolveTexts, ev.NewText)
		}
		evolveEmbeddings, err := e.ai.Embed(ctx, evolveTexts)
		if err != nil {
			return nil, fmt.Errorf("embed evolved facts: %w", err)
		}
		if len(evolveEmbeddings) != len(result.FactsToEvolve) {
			return nil, fmt.Errorf("embed evolved facts: expected %d embeddings, got %d", len(result.FactsToEvolve), len(evolveEmbeddings))
		}
		for idx, ev := range result.FactsToEvolve {
			sourceID := selectSourceIDForExtractedFact(storedSources, 0)
			successor := models.Fact{
				AccountID: input.AccountID,
				AgentID:   ptrString(input.AgentID),
				ThreadID:  ptrString(input.ThreadID),
				SourceID:  sourceID,
				Kind:      ev.NewKind,
				Text:      ev.NewText,
			}
			if len(evolveEmbeddings[idx]) == 0 {
				return nil, fmt.Errorf("embed evolved facts: empty embedding for fact index %d", idx)
			}
			successor.Embedding = evolveEmbeddings[idx]
			inserted, err := e.repo.SupersedeFact(ctx, ev.OldFactID, successor)
			if err != nil {
				return nil, fmt.Errorf("evolve fact %s: %w", ev.OldFactID, err)
			}
			stored = append(stored, *inserted)
		}
	}

	_ = newFacts
	return stored, nil
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

