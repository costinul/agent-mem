package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	models "agentmem/internal/models"
)

func (e *MemoryEngine) AddFactual(ctx context.Context, input models.FactualInput) (models.MemoryOutput, error) {
	if err := validateFactualInput(input); err != nil {
		return models.MemoryOutput{}, err
	}

	log.Printf("factual pipeline start account=%s agent=%s thread=%s inputs=%d", input.AccountID, input.AgentID, input.ThreadID, len(input.Inputs))
	threadID := input.ThreadID
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		ThreadID:  ptrString(threadID),
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, threadID, input.Inputs, false)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	queryEmbeddings, err := e.buildSearchEmbeddings(ctx, decompositions)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	retrieved, err := e.retrieveFacts(ctx, input.AccountID, input.AgentID, threadID, queryEmbeddings)
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

	memInput := models.MemoryInput{
		AccountID:      input.AccountID,
		AgentID:        input.AgentID,
		ThreadID:       threadID,
		IncludeSources: true,
	}
	storedFacts, err := e.applyEvaluateResult(ctx, memInput, storedSources, evalInput, evalResult)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	output, err := e.buildOutput(ctx, memInput, append(evalResult.FactsToReturn, storedFacts...))
	if err != nil {
		return models.MemoryOutput{}, err
	}
	log.Printf("factual pipeline completed event=%s returned_facts=%d", event.ID, len(output.Facts))
	return output, nil
}

func validateFactualInput(input models.FactualInput) error {
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
