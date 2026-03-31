package memoryrepo

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

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

	type candidate struct {
		fact       models.Fact
		similarity float64
	}
	candidates := make([]candidate, 0)
	for _, fact := range r.facts {
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

func (r *InMemoryRepository) DeleteFacts(_ context.Context, factIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range factIDs {
		delete(r.facts, id)
	}
	return nil
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
