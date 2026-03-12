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
		SourceID:  "source-1",
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

func TestInMemoryEventAndSources(t *testing.T) {
	repo := NewInMemory()
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
	if event.ID == "" {
		t.Fatalf("InsertEvent() id is empty")
	}

	text := "Here is the schema for project X"
	src1, err := repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_USER,
		Content:     &text,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}

	bucketPath := "bucket/acct-1/schema.sql"
	size := int64(2048)
	src2, err := repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_CODE,
		BucketPath:  &bucketPath,
		ContentType: "text/plain",
		SizeBytes:   &size,
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}

	sources, err := repo.ListSourcesByEventID(ctx, event.ID)
	if err != nil {
		t.Fatalf("ListSourcesByEventID() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("ListSourcesByEventID() len = %d, want 2", len(sources))
	}

	got, err := repo.GetSourceByID(ctx, src1.ID)
	if err != nil {
		t.Fatalf("GetSourceByID() error = %v", err)
	}
	if got == nil || got.Content == nil || *got.Content != text {
		t.Fatalf("GetSourceByID() content = %v, want %q", got, text)
	}

	_, err = repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		SourceID:  src2.ID,
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "The schema uses UUID primary keys",
	})
	if err != nil {
		t.Fatalf("InsertFact() from source error = %v", err)
	}
}

func TestInMemoryConversationSourcesBySession(t *testing.T) {
	repo := NewInMemory()
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

	userText := "first"
	_, err = repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_USER,
		Content:     &userText,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}

	agentText := "second"
	_, err = repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_AGENT,
		Content:     &agentText,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}

	docPath := "bucket/acct-1/file.pdf"
	_, err = repo.InsertSource(ctx, models.Source{
		EventID:     event.ID,
		Kind:        models.SOURCE_DOCUMENT,
		BucketPath:  &docPath,
		ContentType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}

	sources, err := repo.ListConversationSourcesBySessionID(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("ListConversationSourcesBySessionID() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("ListConversationSourcesBySessionID() len = %d, want 2", len(sources))
	}
	for _, source := range sources {
		if source.Kind != models.SOURCE_USER && source.Kind != models.SOURCE_AGENT {
			t.Fatalf("ListConversationSourcesBySessionID() returned non-conversation kind: %s", source.Kind)
		}
	}
}
