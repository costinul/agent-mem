package engine

import (
	"context"
	"sync/atomic"
	"time"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

type trackerKey struct{}

// CallTracker accumulates DB and AI call durations and counts for a single operation.
type CallTracker struct {
	dbMs    atomic.Int64
	dbCalls atomic.Int64
	aiMs    atomic.Int64
	aiCalls atomic.Int64
}

func (t *CallTracker) addDB(d time.Duration) {
	t.dbMs.Add(d.Milliseconds())
	t.dbCalls.Add(1)
}

func (t *CallTracker) addAI(d time.Duration) {
	t.aiMs.Add(d.Milliseconds())
	t.aiCalls.Add(1)
}

func (t *CallTracker) Stats() models.DurationStats {
	return models.DurationStats{
		DBMs:    t.dbMs.Load(),
		DBCalls: int(t.dbCalls.Load()),
		AIMs:    t.aiMs.Load(),
		AICalls: int(t.aiCalls.Load()),
	}
}

func withTracker(ctx context.Context, t *CallTracker) context.Context {
	return context.WithValue(ctx, trackerKey{}, t)
}

func getTracker(ctx context.Context) *CallTracker {
	if t, ok := ctx.Value(trackerKey{}).(*CallTracker); ok {
		return t
	}
	return nil
}

// repoWrapper wraps a Repository and records the duration of each call into the
// CallTracker stored in the context (when present).
type repoWrapper struct {
	inner memoryrepo.Repository
}

func (w *repoWrapper) observe(ctx context.Context, start time.Time) {
	if t := getTracker(ctx); t != nil {
		t.addDB(time.Since(start))
	}
}

func (w *repoWrapper) InsertEvent(ctx context.Context, event models.Event) (*models.Event, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.InsertEvent(ctx, event)
}

func (w *repoWrapper) ListEventsByThreadID(ctx context.Context, threadID string) ([]models.Event, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListEventsByThreadID(ctx, threadID)
}

func (w *repoWrapper) InsertSource(ctx context.Context, source models.Source) (*models.Source, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.InsertSource(ctx, source)
}

func (w *repoWrapper) GetSourceByID(ctx context.Context, sourceID string) (*models.Source, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.GetSourceByID(ctx, sourceID)
}

func (w *repoWrapper) ListSourcesByEventID(ctx context.Context, eventID string) ([]models.Source, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListSourcesByEventID(ctx, eventID)
}

func (w *repoWrapper) ListConversationSourcesByThreadID(ctx context.Context, threadID string, limit int) ([]models.Source, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListConversationSourcesByThreadID(ctx, threadID, limit)
}

func (w *repoWrapper) InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.InsertFact(ctx, fact)
}

func (w *repoWrapper) ListFactsByScope(ctx context.Context, accountID string, agentID, threadID *string) ([]models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListFactsByScope(ctx, accountID, agentID, threadID)
}

func (w *repoWrapper) ListFactsByThreadID(ctx context.Context, threadID string) ([]models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListFactsByThreadID(ctx, threadID)
}

func (w *repoWrapper) ListFactsBySourceIDs(ctx context.Context, accountID string, sourceIDs []string) ([]models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListFactsBySourceIDs(ctx, accountID, sourceIDs)
}

func (w *repoWrapper) ListFactsFiltered(ctx context.Context, params memoryrepo.ListFactsParams) ([]models.Fact, int, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.ListFactsFiltered(ctx, params)
}

func (w *repoWrapper) GetFactByID(ctx context.Context, factID string) (*models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.GetFactByID(ctx, factID)
}

func (w *repoWrapper) SearchFactsByEmbedding(ctx context.Context, params memoryrepo.SearchByEmbeddingParams) ([]models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SearchFactsByEmbedding(ctx, params)
}

func (w *repoWrapper) SearchFactsByEmbeddingWithScores(ctx context.Context, params memoryrepo.SearchByEmbeddingParams) ([]memoryrepo.FactWithScore, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SearchFactsByEmbeddingWithScores(ctx, params)
}

func (w *repoWrapper) UpdateFact(ctx context.Context, fact models.Fact) error {
	defer w.observe(ctx, time.Now())
	return w.inner.UpdateFact(ctx, fact)
}

func (w *repoWrapper) DeleteFact(ctx context.Context, factID string) error {
	defer w.observe(ctx, time.Now())
	return w.inner.DeleteFact(ctx, factID)
}

func (w *repoWrapper) SupersedeFact(ctx context.Context, oldFactID string, newFact models.Fact) (*models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SupersedeFact(ctx, oldFactID, newFact)
}

func (w *repoWrapper) MaxSourceEventDateForThread(ctx context.Context, threadID string) (*time.Time, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.MaxSourceEventDateForThread(ctx, threadID)
}
