package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func (e *MemoryEngine) ProcessContextual(ctx context.Context, input models.MemoryInput) (models.MemoryOutput, error) {
	if err := validateContextualInput(input); err != nil {
		return models.MemoryOutput{}, err
	}

	log.Printf("contextual pipeline start account=%s agent=%s thread=%s inputs=%d", input.AccountID, input.AgentID, input.ThreadID, len(input.Inputs))
	threadID := input.ThreadID
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		ThreadID:  &threadID,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, input.ThreadID, input.Inputs)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	queryEmbeddings, err := e.buildSearchEmbeddings(ctx, decompositions)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	retrieved, err := e.retrieveFacts(ctx, input.AccountID, input.AgentID, input.ThreadID, queryEmbeddings)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	evalInput := flattenExtractedFacts(decompositions)
	evalResult, err := e.ai.Evaluate(ctx, EvaluateRequest{
		NewFacts:       evalInput,
		RetrievedFacts: retrieved,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("evaluate facts: %w", err)
	}

	storedFacts, err := e.applyEvaluateResult(ctx, input, storedSources, evalInput, evalResult)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	output, err := e.buildOutput(ctx, input, append(evalResult.FactsToReturn, storedFacts...))
	if err != nil {
		return models.MemoryOutput{}, err
	}
	log.Printf("contextual pipeline completed event=%s returned_facts=%d", event.ID, len(output.Facts))
	return output, nil
}

func validateContextualInput(input models.MemoryInput) error {
	if strings.TrimSpace(input.AccountID) == "" {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(input.ThreadID) == "" {
		return errors.New("thread_id is required")
	}
	if strings.TrimSpace(input.AgentID) == "" {
		return errors.New("thread agent is required")
	}
	if len(input.Inputs) == 0 {
		return errors.New("inputs are required")
	}
	for idx, item := range input.Inputs {
		if strings.TrimSpace(item.Content) == "" {
			return fmt.Errorf("inputs[%d].content is required", idx)
		}
		if _, ok := models.SourceTrustHierarchy[item.Kind]; !ok {
			return fmt.Errorf("inputs[%d].kind is invalid", idx)
		}
	}
	return nil
}

func (e *MemoryEngine) buildSearchEmbeddings(ctx context.Context, decompositions []models.Decomposition) ([][]float64, error) {
	queries := make([]string, 0)
	for _, decomposition := range decompositions {
		for _, fact := range decomposition.Facts {
			queries = append(queries, fact.Text)
		}
		for _, query := range decomposition.Queries {
			queries = append(queries, query.Text)
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}
	embeddings, err := e.ai.Embed(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("embed search queries: %w", err)
	}
	return embeddings, nil
}

func (e *MemoryEngine) retrieveFacts(ctx context.Context, accountID, agentID, threadID string, embeddings [][]float64) ([]models.Fact, error) {
	aid := ptrString(agentID)
	tid := ptrString(threadID)
	seen := map[string]struct{}{}
	facts := make([]models.Fact, 0)

	for _, emb := range embeddings {
		threadFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			AgentID:   aid,
			ThreadID:  tid,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search thread facts: %w", err)
		}
		agentFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			AgentID:   aid,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search agent facts: %w", err)
		}
		accountFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search account facts: %w", err)
		}
		facts = appendUniqueFacts(facts, seen, threadFacts...)
		facts = appendUniqueFacts(facts, seen, agentFacts...)
		facts = appendUniqueFacts(facts, seen, accountFacts...)
	}
	return facts, nil
}

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
		if idx < len(embeddings) {
			newFact.Embedding = embeddings[idx]
		}
		inserted, err := e.repo.InsertFact(ctx, newFact)
		if err != nil {
			return nil, fmt.Errorf("insert contextual fact: %w", err)
		}
		stored = append(stored, *inserted)
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
			if idx < len(evolveEmbeddings) {
				successor.Embedding = evolveEmbeddings[idx]
			}
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

func appendUniqueFacts(target []models.Fact, seen map[string]struct{}, facts ...models.Fact) []models.Fact {
	for _, fact := range facts {
		if fact.ID != "" {
			if _, ok := seen[fact.ID]; ok {
				continue
			}
			seen[fact.ID] = struct{}{}
		}
		target = append(target, fact)
	}
	return target
}
