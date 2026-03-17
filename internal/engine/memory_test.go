package engine

import (
	"context"
	"testing"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func TestProcessContextualStoresAndReturnsFacts(t *testing.T) {
	repo := memoryrepo.NewInMemory()
	memEngine := NewMemoryEngine(nil, repo)

	output, err := memEngine.ProcessContextual(context.Background(), models.MemoryInput{
		AccountID:      "acct-1",
		AgentID:        "agent-1",
		SessionID:      "sess-1",
		IncludeSources: true,
		MessageHistory: 5,
		Inputs: []models.InputItem{
			{
				Kind:        models.SOURCE_USER,
				Content:     "I prefer Go for backend services.",
				ContentType: "text/plain",
			},
		},
	})
	if err != nil {
		t.Fatalf("ProcessContextual() error = %v", err)
	}
	if len(output.Facts) == 0 {
		t.Fatalf("ProcessContextual() expected facts")
	}
	if output.Facts[0].Kind != models.FACT_KIND_PREFERENCE {
		t.Fatalf("ProcessContextual() fact kind = %s, want %s", output.Facts[0].Kind, models.FACT_KIND_PREFERENCE)
	}
	hasSource := false
	for _, fact := range output.Facts {
		if fact.OriginalSource != nil {
			hasSource = true
			break
		}
	}
	if !hasSource {
		t.Fatalf("ProcessContextual() expected at least one original source when include_sources=true")
	}
	if len(output.Messages) != 1 {
		t.Fatalf("ProcessContextual() messages len = %d, want 1", len(output.Messages))
	}
}

func TestTrustHierarchyPreventsLowTrustUpdate(t *testing.T) {
	repo := memoryrepo.NewInMemory()
	memEngine := NewMemoryEngine(nil, repo)
	ctx := context.Background()

	sessionID := "sess-1"
	event, err := repo.InsertEvent(ctx, models.Event{
		AccountID: "acct-1",
		AgentID:   "agent-1",
		SessionID: &sessionID,
	})
	if err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}
	content := "user fact"
	source, err := repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_USER,
		Content:     &content,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	agentID := "agent-1"
	fact, err := repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		AgentID:   &agentID,
		SessionID: &sessionID,
		SourceID:  source.ID,
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "critical user requirement",
		Embedding: []float64{1, 0, 0},
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}

	_, err = memEngine.UpdateFact(ctx, fact.ID, "override attempt", models.SOURCE_CODE)
	if err == nil {
		t.Fatalf("UpdateFact() expected trust error")
	}
}

func TestDeleteBlocksSystemFacts(t *testing.T) {
	repo := memoryrepo.NewInMemory()
	memEngine := NewMemoryEngine(nil, repo)
	ctx := context.Background()

	sessionID := "sess-1"
	event, err := repo.InsertEvent(ctx, models.Event{
		AccountID: "acct-1",
		AgentID:   "agent-1",
		SessionID: &sessionID,
	})
	if err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}
	content := "system clock"
	source, err := repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_SYSTEM,
		Content:     &content,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	agentID := "agent-1"
	fact, err := repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		AgentID:   &agentID,
		SessionID: &sessionID,
		SourceID:  source.ID,
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "today is friday",
		Embedding: []float64{1, 0, 0},
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}

	if err := memEngine.DeleteFacts(ctx, []string{fact.ID}, models.SOURCE_USER); err == nil {
		t.Fatalf("DeleteFacts() expected immutable system error")
	}
}
