package memoryrepo

import (
	"context"
	"testing"

	models "agentmem/internal/models"
)

func TestInMemoryFactLifecycle(t *testing.T) {
	repo := NewInMemory()
	ctx := context.Background()

	inserted, err := repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		EventID:   "event-1",
		Source:    models.SOURCE_USER,
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}
	if inserted.ID == "" {
		t.Fatalf("InsertFact() id is empty")
	}

	got, err := repo.GetFactByID(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetFactByID() error = %v", err)
	}
	if got == nil || got.Text != "hello" {
		t.Fatalf("GetFactByID() text = %v, want %q", got, "hello")
	}

	got.Text = "updated"
	if err := repo.UpdateFact(ctx, *got); err != nil {
		t.Fatalf("UpdateFact() error = %v", err)
	}

	updated, err := repo.GetFactByID(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetFactByID() after update error = %v", err)
	}
	if updated.Text != "updated" {
		t.Fatalf("GetFactByID() updated text = %q, want %q", updated.Text, "updated")
	}

	if err := repo.DeleteFacts(ctx, []string{inserted.ID}); err != nil {
		t.Fatalf("DeleteFacts() error = %v", err)
	}
	deleted, err := repo.GetFactByID(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetFactByID() after delete error = %v", err)
	}
	if deleted != nil {
		t.Fatalf("GetFactByID() after delete = %v, want nil", deleted)
	}
}

func TestInMemoryFactLinksAndMessages(t *testing.T) {
	repo := NewInMemory()
	ctx := context.Background()

	fact, err := repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		EventID:   "event-1",
		Source:    models.SOURCE_USER,
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "fact",
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}

	_, err = repo.InsertFactLink(ctx, models.FactLink{
		FactID:    fact.ID,
		EventID:   "event-1",
		InputHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("InsertFactLink() error = %v", err)
	}
	links, err := repo.ListFactLinksByFactID(ctx, fact.ID)
	if err != nil {
		t.Fatalf("ListFactLinksByFactID() error = %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("ListFactLinksByFactID() len = %d, want 1", len(links))
	}

	_, err = repo.InsertRawMessage(ctx, models.RawMessage{
		SessionID: "sess-1",
		EventID:   "event-1",
		Source:    models.SOURCE_USER,
		Content:   "first",
		Sequence:  1,
	})
	if err != nil {
		t.Fatalf("InsertRawMessage() error = %v", err)
	}
	_, err = repo.InsertRawMessage(ctx, models.RawMessage{
		SessionID: "sess-1",
		EventID:   "event-2",
		Source:    models.SOURCE_AGENT,
		Content:   "second",
		Sequence:  2,
	})
	if err != nil {
		t.Fatalf("InsertRawMessage() error = %v", err)
	}

	msgs, err := repo.ListRawMessagesBySessionID(ctx, "sess-1", 1)
	if err != nil {
		t.Fatalf("ListRawMessagesBySessionID() error = %v", err)
	}
	if len(msgs) != 1 || msgs[0].Sequence != 2 {
		t.Fatalf("ListRawMessagesBySessionID() = %+v, want latest sequence 2 only", msgs)
	}
}
