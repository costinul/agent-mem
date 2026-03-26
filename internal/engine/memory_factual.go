package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	models "agentmem/internal/models"
)

func (e *MemoryEngine) AddFactual(ctx context.Context, input models.FactualInput) (models.MemoryOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.AgentID) == "" {
		return models.MemoryOutput{}, errors.New("account_id and agent_id are required")
	}
	if len(input.Inputs) == 0 {
		return models.MemoryOutput{}, errors.New("inputs are required")
	}
	for idx, item := range input.Inputs {
		if strings.TrimSpace(item.Content) == "" {
			return models.MemoryOutput{}, fmt.Errorf("inputs[%d].content is required", idx)
		}
		if _, ok := models.SourceTrustHierarchy[item.Kind]; !ok {
			return models.MemoryOutput{}, fmt.Errorf("inputs[%d].kind is invalid", idx)
		}
	}

	var sessionID *string
	if strings.TrimSpace(input.SessionID) != "" {
		s := strings.TrimSpace(input.SessionID)
		sessionID = &s
	}
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		SessionID: sessionID,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, strings.TrimSpace(input.SessionID), input.Inputs)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	extracted := flattenExtractedFacts(decompositions)
	texts := make([]string, 0, len(extracted))
	for _, fact := range extracted {
		texts = append(texts, fact.Text)
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("embed factual facts: %w", err)
	}

	stored := make([]models.Fact, 0, len(extracted))
	for idx, extractedFact := range extracted {
		sourceID := selectSourceIDForExtractedFact(storedSources, idx)
		fact := models.Fact{
			AccountID: input.AccountID,
			AgentID:   ptrString(input.AgentID),
			SessionID: sessionID,
			SourceID:  sourceID,
			Kind:      extractedFact.Kind,
			Text:      extractedFact.Text,
			Embedding: embeddings[idx],
		}
		inserted, err := e.repo.InsertFact(ctx, fact)
		if err != nil {
			return models.MemoryOutput{}, fmt.Errorf("insert factual fact: %w", err)
		}
		stored = append(stored, *inserted)
	}

	return e.buildOutput(ctx, models.MemoryInput{
		IncludeSources: true,
	}, stored)
}
