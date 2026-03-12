package memoryrepo

import (
	"context"
	"sort"
	"sync"
	"time"

	models "agentmem/internal/models"

	"github.com/google/uuid"
)

type InMemoryRepository struct {
	mu       sync.RWMutex
	events   map[string]models.Event
	sources  map[string]models.Source
	facts    map[string]models.Fact
}

func NewInMemory() *InMemoryRepository {
	return &InMemoryRepository{
		events:   make(map[string]models.Event),
		sources:  make(map[string]models.Source),
		facts:    make(map[string]models.Fact),
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

func (r *InMemoryRepository) ListConversationSourcesBySessionID(_ context.Context, sessionID string, limit int) ([]models.Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]models.Source, 0)
	for _, source := range r.sources {
		event, ok := r.events[source.EventID]
		if !ok || event.SessionID == nil || *event.SessionID != sessionID {
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

func (r *InMemoryRepository) DeleteFacts(_ context.Context, factIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range factIDs {
		delete(r.facts, id)
	}
	return nil
}

