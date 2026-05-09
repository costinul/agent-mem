package engine

import (
	"context"
	"testing"

	"agentmem/internal/config"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func newZeroTestEngine() *MemoryEngine {
	return NewMemoryEngine(nil, memoryrepo.NewInMemory(), LLMModels{}, "", config.IngestionConfig{ChunkMaxTokens: 4000, ChunkOverlapTokens: 400}, DefaultRecall(), NewTrackerRegistry())
}

func TestRecallZero_MissingAccountIDReturnsValidationError(t *testing.T) {
	eng := newZeroTestEngine()
	_, err := eng.RecallZero(context.Background(), models.RecallInput{Query: "something"})
	if err == nil {
		t.Fatal("expected error for missing account_id")
	}
}

func TestRecallZero_MissingQueryReturnsValidationError(t *testing.T) {
	eng := newZeroTestEngine()
	_, err := eng.RecallZero(context.Background(), models.RecallInput{AccountID: "acc1"})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestRecallZero_BlankQueryReturnsValidationError(t *testing.T) {
	eng := newZeroTestEngine()
	_, err := eng.RecallZero(context.Background(), models.RecallInput{AccountID: "acc1", Query: "   "})
	if err == nil {
		t.Fatal("expected error for blank query")
	}
}
