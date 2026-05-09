package memoryrepo

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	models "agentmem/internal/models"

	"github.com/google/uuid"
)

type InMemoryRepository struct {
	mu      sync.RWMutex
	events  map[string]models.Event
	sources map[string]models.Source
	facts   map[string]models.Fact
}

func NewInMemory() *InMemoryRepository {
	return &InMemoryRepository{
		events:  make(map[string]models.Event),
		sources: make(map[string]models.Source),
		facts:   make(map[string]models.Fact),
	}
}

// =====================
// Events
// =====================

func (r *InMemoryRepository) InsertEvent(_ context.Context, event models.Event) (*models.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	r.events[event.ID] = event

	stored := event
	return &stored, nil
}

func (r *InMemoryRepository) ListEventsByThreadID(_ context.Context, threadID string) ([]models.Event, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := make([]models.Event, 0)
	for _, e := range r.events {
		if e.ThreadID != nil && *e.ThreadID == threadID {
			events = append(events, e)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	return events, nil
}

// =====================
// Sources
// =====================

func (r *InMemoryRepository) InsertSource(_ context.Context, source models.Source) (*models.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if source.ID == "" {
		source.ID = uuid.NewString()
	}
	if source.CreatedAt.IsZero() {
		source.CreatedAt = time.Now().UTC()
	}
	r.sources[source.ID] = source

	stored := source
	return &stored, nil
}

func (r *InMemoryRepository) GetSourceByID(_ context.Context, sourceID string) (*models.Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	source, ok := r.sources[sourceID]
	if !ok {
		return nil, nil
	}
	copy := source
	return &copy, nil
}

func (r *InMemoryRepository) ListSourcesByEventID(_ context.Context, eventID string) ([]models.Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]models.Source, 0)
	for _, s := range r.sources {
		if s.EventID == eventID {
			sources = append(sources, s)
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].CreatedAt.Before(sources[j].CreatedAt)
	})
	return sources, nil
}

func (r *InMemoryRepository) ListConversationSourcesByThreadID(_ context.Context, threadID string, limit int) ([]models.Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]models.Source, 0)
	for _, source := range r.sources {
		event, ok := r.events[source.EventID]
		if !ok || event.ThreadID == nil || *event.ThreadID != threadID {
			continue
		}
		if source.Kind != models.SOURCE_USER && source.Kind != models.SOURCE_AGENT {
			continue
		}
		sources = append(sources, source)
	}

	sort.Slice(sources, func(i, j int) bool {
		return sources[i].CreatedAt.Before(sources[j].CreatedAt)
	})

	if limit > 0 && len(sources) > limit {
		sources = sources[len(sources)-limit:]
	}
	return sources, nil
}

func (r *InMemoryRepository) SearchSourcesByContent(_ context.Context, accountID, agentID, threadID, text string) ([]models.Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []models.Source
	for _, s := range r.sources {
		if s.Content == nil || !strings.HasPrefix(*s.Content, text) {
			continue
		}
		e, ok := r.events[s.EventID]
		if !ok || e.AccountID != accountID {
			continue
		}
		if agentID != "" && e.AgentID != agentID {
			continue
		}
		if threadID != "" && (e.ThreadID == nil || *e.ThreadID != threadID) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// =====================
// Facts
// =====================

func (r *InMemoryRepository) InsertFact(_ context.Context, fact models.Fact) (*models.Fact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if fact.ID == "" {
		fact.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = now
	}
	fact.UpdatedAt = now
	r.facts[fact.ID] = fact

	stored := fact
	return &stored, nil
}

func (r *InMemoryRepository) GetFactByID(_ context.Context, factID string) (*models.Fact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fact, ok := r.facts[factID]
	if !ok {
		return nil, nil
	}
	copy := fact
	return &copy, nil
}

func (r *InMemoryRepository) ListFactsByScope(_ context.Context, accountID string, agentID, threadID *string) ([]models.Fact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	facts := make([]models.Fact, 0)
	for _, fact := range r.facts {
		if fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != accountID {
			continue
		}
		if agentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *agentID {
				continue
			}
		}
		if threadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *threadID {
				continue
			}
		}
		facts = append(facts, fact)
	}
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].CreatedAt.Before(facts[j].CreatedAt)
	})
	return facts, nil
}

// ListFactsBySourceIDs returns every fact (including superseded ones) for the given
// source IDs scoped to the account. Superseded facts are intentionally included so
// callers (e.g. recall sibling expansion) can surface historical context.
func (r *InMemoryRepository) ListFactsBySourceIDs(_ context.Context, accountID string, sourceIDs []string) ([]models.Fact, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	set := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		set[id] = struct{}{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	facts := make([]models.Fact, 0)
	for _, fact := range r.facts {
		if fact.AccountID != accountID {
			continue
		}
		if _, ok := set[fact.SourceID]; !ok {
			continue
		}
		facts = append(facts, fact)
	}
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].CreatedAt.Before(facts[j].CreatedAt)
	})
	return facts, nil
}

func (r *InMemoryRepository) ListFactsByThreadID(_ context.Context, threadID string) ([]models.Fact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	facts := make([]models.Fact, 0)
	for _, f := range r.facts {
		if f.ThreadID != nil && *f.ThreadID == threadID {
			facts = append(facts, f)
		}
	}
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].CreatedAt.Before(facts[j].CreatedAt)
	})
	return facts, nil
}

func (r *InMemoryRepository) ListFactsFiltered(_ context.Context, params ListFactsParams) ([]models.Fact, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]models.Fact, 0)
	for _, fact := range r.facts {
		if fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != params.AccountID {
			continue
		}
		if params.AgentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *params.AgentID {
				continue
			}
		}
		if params.ThreadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *params.ThreadID {
				continue
			}
		}
		if params.Kind != nil && fact.Kind != *params.Kind {
			continue
		}
		matched = append(matched, fact)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(matched) {
		return []models.Fact{}, total, nil
	}
	end := offset + limit
	if end > len(matched) {
		end = len(matched)
	}
	return matched[offset:end], total, nil
}

func (r *InMemoryRepository) DeleteFact(_ context.Context, factID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.facts, factID)
	return nil
}

func (r *InMemoryRepository) UpdateFact(_ context.Context, fact models.Fact) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.facts[fact.ID]
	if !ok {
		return nil
	}
	fact.CreatedAt = existing.CreatedAt
	fact.UpdatedAt = time.Now().UTC()
	r.facts[fact.ID] = fact
	return nil
}

func (r *InMemoryRepository) SearchFactsByEmbedding(_ context.Context, params SearchByEmbeddingParams) ([]models.Fact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(params.Embedding) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.MinSimilarity <= 0 {
		params.MinSimilarity = 0.65
	}

	sourceSet := make(map[string]struct{}, len(params.SourceIDs))
	for _, id := range params.SourceIDs {
		sourceSet[id] = struct{}{}
	}

	type candidate struct {
		fact       models.Fact
		similarity float64
	}
	candidates := make([]candidate, 0)
	for _, fact := range r.facts {
		if !params.IncludeSuperseded && fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != params.AccountID {
			continue
		}
		if params.AgentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *params.AgentID {
				continue
			}
		}
		if params.ThreadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *params.ThreadID {
				continue
			}
		}
		if len(sourceSet) > 0 {
			if _, ok := sourceSet[fact.SourceID]; !ok {
				continue
			}
		}
		if params.MaxSourceEventDate != nil {
			if src, ok := r.sources[fact.SourceID]; ok && src.EventDate.After(*params.MaxSourceEventDate) {
				continue
			}
		}
		similarity := cosineSimilarity(params.Embedding, fact.Embedding)
		if similarity >= params.MinSimilarity {
			candidates = append(candidates, candidate{fact: fact, similarity: similarity})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].similarity == candidates[j].similarity {
			return candidates[i].fact.CreatedAt.Before(candidates[j].fact.CreatedAt)
		}
		return candidates[i].similarity > candidates[j].similarity
	})

	if len(candidates) > params.Limit {
		candidates = candidates[:params.Limit]
	}

	result := make([]models.Fact, 0, len(candidates))
	for _, item := range candidates {
		result = append(result, item.fact)
	}
	return result, nil
}

func (r *InMemoryRepository) SearchFactsByEmbeddingWithScores(_ context.Context, params SearchByEmbeddingParams) ([]FactWithScore, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(params.Embedding) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	sourceSet := make(map[string]struct{}, len(params.SourceIDs))
	for _, id := range params.SourceIDs {
		sourceSet[id] = struct{}{}
	}

	type candidate struct {
		fact  models.Fact
		score float64
	}
	candidates := make([]candidate, 0)
	for _, fact := range r.facts {
		if !params.IncludeSuperseded && fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != params.AccountID {
			continue
		}
		if params.AgentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *params.AgentID {
				continue
			}
		}
		if params.ThreadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *params.ThreadID {
				continue
			}
		}
		if len(sourceSet) > 0 {
			if _, ok := sourceSet[fact.SourceID]; !ok {
				continue
			}
		}
		if params.MaxSourceEventDate != nil {
			if src, ok := r.sources[fact.SourceID]; ok && src.EventDate.After(*params.MaxSourceEventDate) {
				continue
			}
		}
		candidates = append(candidates, candidate{fact: fact, score: cosineSimilarity(params.Embedding, fact.Embedding)})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].fact.CreatedAt.Before(candidates[j].fact.CreatedAt)
		}
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > params.Limit {
		candidates = candidates[:params.Limit]
	}

	result := make([]FactWithScore, 0, len(candidates))
	for _, item := range candidates {
		result = append(result, FactWithScore{Fact: item.fact, Score: item.score})
	}
	return result, nil
}

func (r *InMemoryRepository) SupersedeFact(_ context.Context, oldFactID string, newFact models.Fact, supersededAt time.Time) (*models.Fact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if newFact.ID == "" {
		newFact.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if newFact.CreatedAt.IsZero() {
		newFact.CreatedAt = now
	}
	newFact.UpdatedAt = now
	r.facts[newFact.ID] = newFact

	boundary := supersededAt.UTC()
	if old, ok := r.facts[oldFactID]; ok {
		old.SupersededAt = &boundary
		old.SupersededBy = &newFact.ID
		old.UpdatedAt = now
		r.facts[oldFactID] = old
	}

	stored := newFact
	return &stored, nil
}

func (r *InMemoryRepository) MaxSourceEventDateForThread(ctx context.Context, threadID string) (*time.Time, error) {
	var max *time.Time
	for _, s := range r.sources {
		if e, ok := r.events[s.EventID]; ok {
			if e.ThreadID != nil && *e.ThreadID == threadID {
				t := s.EventDate
				if max == nil || t.After(*max) {
					max = &t
				}
			}
		}
	}
	return max, nil
}

// SearchFactsByText runs an in-memory tf-idf scoring against fact text. Used by
// tests; the production path is Postgres tsvector. Tokens are lowercase
// alphanumeric runs; idf is log((N+1)/(df+1)) over the candidate set, tf is raw count.
func (r *InMemoryRepository) SearchFactsByText(_ context.Context, params SearchByTextParams) ([]FactWithScore, error) {
	tokens := tokenizeText(params.Query)
	if len(tokens) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := make([]models.Fact, 0)
	for _, fact := range r.facts {
		if !params.IncludeSuperseded && fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != params.AccountID {
			continue
		}
		if params.AgentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *params.AgentID {
				continue
			}
		}
		if params.ThreadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *params.ThreadID {
				continue
			}
		}
		if params.MaxSourceEventDate != nil {
			if src, ok := r.sources[fact.SourceID]; ok && src.EventDate.After(*params.MaxSourceEventDate) {
				continue
			}
		}
		candidates = append(candidates, fact)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	df := make(map[string]int, len(tokens))
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = struct{}{}
	}
	docTokens := make([][]string, len(candidates))
	for i, fact := range candidates {
		docTokens[i] = tokenizeText(fact.Text)
		seen := make(map[string]struct{}, len(docTokens[i]))
		for _, w := range docTokens[i] {
			if _, ok := tokenSet[w]; !ok {
				continue
			}
			if _, dup := seen[w]; dup {
				continue
			}
			seen[w] = struct{}{}
			df[w]++
		}
	}

	type scored struct {
		fact  models.Fact
		score float64
	}
	scoredFacts := make([]scored, 0, len(candidates))
	N := float64(len(candidates))
	for i, fact := range candidates {
		tf := make(map[string]int, len(docTokens[i]))
		for _, w := range docTokens[i] {
			if _, ok := tokenSet[w]; ok {
				tf[w]++
			}
		}
		score := 0.0
		for _, t := range tokens {
			if tf[t] == 0 {
				continue
			}
			idf := math.Log((N + 1.0) / (float64(df[t]) + 1.0))
			score += float64(tf[t]) * idf
		}
		if score > 0 {
			scoredFacts = append(scoredFacts, scored{fact: fact, score: score})
		}
	}

	sort.Slice(scoredFacts, func(i, j int) bool {
		if scoredFacts[i].score == scoredFacts[j].score {
			return scoredFacts[i].fact.CreatedAt.Before(scoredFacts[j].fact.CreatedAt)
		}
		return scoredFacts[i].score > scoredFacts[j].score
	})
	if len(scoredFacts) > params.Limit {
		scoredFacts = scoredFacts[:params.Limit]
	}
	out := make([]FactWithScore, 0, len(scoredFacts))
	for _, s := range scoredFacts {
		out = append(out, FactWithScore{Fact: s.fact, Score: s.score})
	}
	return out, nil
}

// SearchFactsByEntities returns facts whose stored entities overlap the queried
// entities. Score = #matched / #queried. Both sides are compared lowercased.
func (r *InMemoryRepository) SearchFactsByEntities(_ context.Context, params SearchByEntitiesParams) ([]FactWithScore, error) {
	if len(params.Entities) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	queried := make(map[string]struct{}, len(params.Entities))
	for _, e := range params.Entities {
		s := strings.ToLower(strings.TrimSpace(e))
		if s != "" {
			queried[s] = struct{}{}
		}
	}
	if len(queried) == 0 {
		return nil, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	type scored struct {
		fact  models.Fact
		score float64
	}
	out := make([]scored, 0)
	for _, fact := range r.facts {
		if !params.IncludeSuperseded && fact.SupersededAt != nil {
			continue
		}
		if fact.AccountID != params.AccountID {
			continue
		}
		if params.AgentID != nil {
			if fact.AgentID == nil || *fact.AgentID != *params.AgentID {
				continue
			}
		}
		if params.ThreadID != nil {
			if fact.ThreadID == nil || *fact.ThreadID != *params.ThreadID {
				continue
			}
		}
		if params.MaxSourceEventDate != nil {
			if src, ok := r.sources[fact.SourceID]; ok && src.EventDate.After(*params.MaxSourceEventDate) {
				continue
			}
		}
		matched := 0
		seen := make(map[string]struct{}, len(fact.Entities))
		for _, e := range fact.Entities {
			s := strings.ToLower(strings.TrimSpace(e))
			if s == "" {
				continue
			}
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			if _, ok := queried[s]; ok {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		out = append(out, scored{fact: fact, score: float64(matched) / float64(len(queried))})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].score == out[j].score {
			return out[i].fact.CreatedAt.Before(out[j].fact.CreatedAt)
		}
		return out[i].score > out[j].score
	})
	if len(out) > params.Limit {
		out = out[:params.Limit]
	}
	results := make([]FactWithScore, 0, len(out))
	for _, s := range out {
		results = append(results, FactWithScore{Fact: s.fact, Score: s.score})
	}
	return results, nil
}

// tokenizeText splits free text into lowercase alphanumeric tokens. Anything that is
// not a letter or digit acts as a separator. This is intentionally simple — Postgres
// does the real lexical work in production, in-memory is just for tests.
func tokenizeText(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 8)
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
