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

	successor, err := repo.SupersedeFact(ctx, inserted.ID, models.Fact{
		AccountID: "acct-1",
		SourceID:  "source-1",
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "previously: hello",
		Embedding: []float64{1, 0, 0},
	})
	if err != nil {
		t.Fatalf("SupersedeFact() error = %v", err)
	}
	if successor == nil || successor.Text != "previously: hello" {
		t.Fatalf("SupersedeFact() successor = %v, want text %q", successor, "previously: hello")
	}

	old, err := repo.GetFactByID(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetFactByID() after supersede error = %v", err)
	}
	if old == nil {
		t.Fatal("GetFactByID() after supersede returned nil, want superseded fact")
	}
	if old.SupersededAt == nil {
		t.Fatal("superseded fact should have SupersededAt set")
	}
	if old.SupersededBy == nil || *old.SupersededBy != successor.ID {
		t.Fatalf("superseded fact SupersededBy = %v, want %s", old.SupersededBy, successor.ID)
	}

	results, err := repo.SearchFactsByEmbedding(ctx, SearchByEmbeddingParams{
		AccountID:     "acct-1",
		Embedding:     []float64{1, 0, 0},
		MinSimilarity: 0.5,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("SearchFactsByEmbedding() after supersede error = %v", err)
	}
	for _, r := range results {
		if r.ID == inserted.ID {
			t.Fatal("superseded fact should be excluded from search results")
		}
	}
}

func TestInMemoryEventAndSources(t *testing.T) {
	repo := NewInMemory()
	ctx := context.Background()

	threadID := "thread-1"
	event, err := repo.InsertEvent(ctx, models.Event{
		AccountID: "acct-1",
		AgentID:   "agent-1",
		ThreadID:  &threadID,
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

func TestInMemoryConversationSourcesByThread(t *testing.T) {
	repo := NewInMemory()
	ctx := context.Background()

	threadID := "thread-1"
	event, err := repo.InsertEvent(ctx, models.Event{
		AccountID: "acct-1",
		AgentID:   "agent-1",
		ThreadID:  &threadID,
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

	sources, err := repo.ListConversationSourcesByThreadID(ctx, threadID, 10)
	if err != nil {
		t.Fatalf("ListConversationSourcesByThreadID() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("ListConversationSourcesByThreadID() len = %d, want 2", len(sources))
	}
	for _, source := range sources {
		if source.Kind != models.SOURCE_USER && source.Kind != models.SOURCE_AGENT {
			t.Fatalf("ListConversationSourcesByThreadID() returned non-conversation kind: %s", source.Kind)
		}
	}
}

func TestInMemorySearchFactsByEmbedding(t *testing.T) {
	repo := NewInMemory()
	ctx := context.Background()

	agentID := "agent-1"
	threadID := "thread-1"
	_, err := repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		AgentID:   &agentID,
		ThreadID:  &threadID,
		SourceID:  "source-1",
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "postgres is primary db",
		Embedding: []float64{1, 0, 0},
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}
	_, err = repo.InsertFact(ctx, models.Fact{
		AccountID: "acct-1",
		SourceID:  "source-2",
		Kind:      models.FACT_KIND_KNOWLEDGE,
		Text:      "redis cache",
		Embedding: []float64{0, 1, 0},
	})
	if err != nil {
		t.Fatalf("InsertFact() error = %v", err)
	}

	results, err := repo.SearchFactsByEmbedding(ctx, SearchByEmbeddingParams{
		AccountID:     "acct-1",
		AgentID:       &agentID,
		ThreadID:      &threadID,
		Embedding:     []float64{1, 0, 0},
		MinSimilarity: 0.7,
		Limit:         5,
	})
	if err != nil {
		t.Fatalf("SearchFactsByEmbedding() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchFactsByEmbedding() len = %d, want 1", len(results))
	}
	if results[0].Text != "postgres is primary db" {
		t.Fatalf("SearchFactsByEmbedding() text = %q", results[0].Text)
	}
}
