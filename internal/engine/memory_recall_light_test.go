package engine

import (
	"context"
	"testing"

	"agentmem/internal/config"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func newLightTestEngine() *MemoryEngine {
	return NewMemoryEngine(nil, memoryrepo.NewInMemory(), LLMModels{}, "", config.IngestionConfig{ChunkMaxTokens: 4000, ChunkOverlapTokens: 400}, NewTrackerRegistry())
}

func TestRecallLight_MissingAccountIDReturnsValidationError(t *testing.T) {
	eng := newLightTestEngine()
	_, err := eng.RecallLight(context.Background(), models.RecallInput{Query: "something"})
	if err == nil {
		t.Fatal("expected error for missing account_id")
	}
}

func TestRecallLight_MissingQueryReturnsValidationError(t *testing.T) {
	eng := newLightTestEngine()
	_, err := eng.RecallLight(context.Background(), models.RecallInput{AccountID: "acc1"})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestRecallLight_BlankQueryReturnsValidationError(t *testing.T) {
	eng := newLightTestEngine()
	_, err := eng.RecallLight(context.Background(), models.RecallInput{AccountID: "acc1", Query: "   "})
	if err == nil {
		t.Fatal("expected error for blank query")
	}
}
