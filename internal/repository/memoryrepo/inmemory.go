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
	facts    map[string]models.Fact
	links    map[string]models.FactLink
	messages map[string][]models.RawMessage
}

func NewInMemory() *InMemoryRepository {
	return &InMemoryRepository{
		facts:    make(map[string]models.Fact),
		links:    make(map[string]models.FactLink),
		messages: make(map[string][]models.RawMessage),
	}
}

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
	copyFact := fact
	return &copyFact, nil
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

func (r *InMemoryRepository) InsertFactLink(_ context.Context, link models.FactLink) (*models.FactLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if link.ID == "" {
		link.ID = uuid.NewString()
	}
	r.links[link.ID] = link

	stored := link
	return &stored, nil
}

func (r *InMemoryRepository) ListFactLinksByFactID(_ context.Context, factID string) ([]models.FactLink, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	links := make([]models.FactLink, 0)
	for _, link := range r.links {
		if link.FactID == factID {
			links = append(links, link)
		}
	}
	return links, nil
}

func (r *InMemoryRepository) InsertRawMessage(_ context.Context, msg models.RawMessage) (*models.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	r.messages[msg.SessionID] = append(r.messages[msg.SessionID], msg)
	stored := msg
	return &stored, nil
}

func (r *InMemoryRepository) ListRawMessagesBySessionID(_ context.Context, sessionID string, limit int) ([]models.RawMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	msgs := append([]models.RawMessage(nil), r.messages[sessionID]...)
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Sequence < msgs[j].Sequence
	})

	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}
