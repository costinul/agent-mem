package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

type trackerKey struct{}

// CallTracker accumulates DB, LLM, and embedding call durations, counts, and token usage
// for a single API request.
type CallTracker struct {
	dbMs       atomic.Int64
	dbCalls    atomic.Int64
	llmMs      atomic.Int64
	llmCalls   atomic.Int64
	embedMs    atomic.Int64
	embedCalls atomic.Int64

	inputTokens  atomic.Int64
	outputTokens atomic.Int64

	// perModel is populated only when debugMode is true.
	debugMode bool
	mu        sync.Mutex
	perModel  map[string]*modelUsage
}

type modelUsage struct {
	calls        int64
	inputTokens  int64
	outputTokens int64
}

// NewCallTracker creates a tracker. When debug is true the per-model breakdown
// is collected; otherwise only aggregate token totals are tracked.
func NewCallTracker(debug bool) *CallTracker {
	t := &CallTracker{debugMode: debug}
	if debug {
		t.perModel = map[string]*modelUsage{}
	}
	return t
}

func (t *CallTracker) addTokens(model string, in, out int) {
	t.inputTokens.Add(int64(in))
	t.outputTokens.Add(int64(out))
	if !t.debugMode {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.perModel[model]
	if m == nil {
		m = &modelUsage{}
		t.perModel[model] = m
	}
	m.calls++
	m.inputTokens += int64(in)
	m.outputTokens += int64(out)
}

// Usage returns the token stats for the request. PerModel is non-nil only
// when the tracker was created with debug=true.
func (t *CallTracker) Usage() models.TokenStats {
	s := models.TokenStats{
		InputTokens:  t.inputTokens.Load(),
		OutputTokens: t.outputTokens.Load(),
	}
	if t.debugMode {
		t.mu.Lock()
		defer t.mu.Unlock()
		if len(t.perModel) > 0 {
			s.PerModel = make(map[string]models.ModelUsage, len(t.perModel))
			for model, u := range t.perModel {
				s.PerModel[model] = models.ModelUsage{
					Calls:        u.calls,
					InputTokens:  u.inputTokens,
					OutputTokens: u.outputTokens,
				}
			}
		}
	}
	return s
}

func (t *CallTracker) addDB(d time.Duration) {
	t.dbMs.Add(d.Milliseconds())
	t.dbCalls.Add(1)
}

func (t *CallTracker) addLLM(d time.Duration) {
	t.llmMs.Add(d.Milliseconds())
	t.llmCalls.Add(1)
}

func (t *CallTracker) addEmbed(d time.Duration) {
	t.embedMs.Add(d.Milliseconds())
	t.embedCalls.Add(1)
}

func (t *CallTracker) Stats() models.DurationStats {
	return models.DurationStats{
		DBMs:       t.dbMs.Load(),
		DBCalls:    int(t.dbCalls.Load()),
		LLMMs:      t.llmMs.Load(),
		LLMCalls:   int(t.llmCalls.Load()),
		EmbedMs:    t.embedMs.Load(),
		EmbedCalls: int(t.embedCalls.Load()),
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

func (w *repoWrapper) SearchFactsByText(ctx context.Context, params memoryrepo.SearchByTextParams) ([]memoryrepo.FactWithScore, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SearchFactsByText(ctx, params)
}

func (w *repoWrapper) SearchFactsByEntities(ctx context.Context, params memoryrepo.SearchByEntitiesParams) ([]memoryrepo.FactWithScore, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SearchFactsByEntities(ctx, params)
}

func (w *repoWrapper) UpdateFact(ctx context.Context, fact models.Fact) error {
	defer w.observe(ctx, time.Now())
	return w.inner.UpdateFact(ctx, fact)
}

func (w *repoWrapper) DeleteFact(ctx context.Context, factID string) error {
	defer w.observe(ctx, time.Now())
	return w.inner.DeleteFact(ctx, factID)
}

func (w *repoWrapper) SupersedeFact(ctx context.Context, oldFactID string, newFact models.Fact, supersededAt time.Time) (*models.Fact, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SupersedeFact(ctx, oldFactID, newFact, supersededAt)
}

func (w *repoWrapper) MaxSourceEventDateForThread(ctx context.Context, threadID string) (*time.Time, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.MaxSourceEventDateForThread(ctx, threadID)
}

func (w *repoWrapper) SearchSourcesByContent(ctx context.Context, accountID, agentID, threadID, text string) ([]models.Source, error) {
	defer w.observe(ctx, time.Now())
	return w.inner.SearchSourcesByContent(ctx, accountID, agentID, threadID, text)
}
