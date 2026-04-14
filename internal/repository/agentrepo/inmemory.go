package agentrepo

import (
	"context"
	"sync"
	"time"

	models "agentmem/internal/models"

	"github.com/google/uuid"
)

type InMemoryRepository struct {
	mu      sync.RWMutex
	agents  map[string]models.Agent
	threads map[string]models.Thread
}

func NewInMemory() *InMemoryRepository {
	return &InMemoryRepository{
		agents:  make(map[string]models.Agent),
		threads: make(map[string]models.Thread),
	}
}

func (r *InMemoryRepository) CreateAgent(_ context.Context, accountID, name string) (*models.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	agent := models.Agent{
		ID:        uuid.New().String(),
		AccountID: accountID,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.agents[agent.ID] = agent
	copy := agent
	return &copy, nil
}

func (r *InMemoryRepository) GetAgentByID(_ context.Context, accountID, agentID string) (*models.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[agentID]
	if !ok || agent.AccountID != accountID {
		return nil, nil
	}
	copy := agent
	return &copy, nil
}

func (r *InMemoryRepository) DeleteAgentByID(_ context.Context, accountID, agentID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.agents[agentID]
	if !ok || agent.AccountID != accountID {
		return false, nil
	}
	delete(r.agents, agentID)
	return true, nil
}

func (r *InMemoryRepository) ListAllAgents(_ context.Context, accountID string) ([]models.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]models.Agent, 0)
	for _, a := range r.agents {
		if accountID == "" || a.AccountID == accountID {
			agents = append(agents, a)
		}
	}
	return agents, nil
}

func (r *InMemoryRepository) UpdateAgent(_ context.Context, accountID, agentID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[agentID]
	if !ok || a.AccountID != accountID {
		return nil
	}
	a.Name = name
	a.UpdatedAt = time.Now().UTC()
	r.agents[agentID] = a
	return nil
}

func (r *InMemoryRepository) CreateThread(_ context.Context, accountID, agentID string) (*models.Thread, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	thread := models.Thread{
		ID:        uuid.New().String(),
		AccountID: accountID,
		AgentID:   agentID,
		CreatedAt: time.Now().UTC(),
	}
	r.threads[thread.ID] = thread
	copy := thread
	return &copy, nil
}

func (r *InMemoryRepository) GetThreadByID(_ context.Context, accountID, threadID string) (*models.Thread, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	thread, ok := r.threads[threadID]
	if !ok || thread.AccountID != accountID {
		return nil, nil
	}
	copy := thread
	return &copy, nil
}

func (r *InMemoryRepository) DeleteThreadByID(_ context.Context, accountID, threadID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	thread, ok := r.threads[threadID]
	if !ok || thread.AccountID != accountID {
		return false, nil
	}
	delete(r.threads, threadID)
	return true, nil
}

func (r *InMemoryRepository) ListAllThreads(_ context.Context, accountID string, agentID *string) ([]models.Thread, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	threads := make([]models.Thread, 0)
	for _, t := range r.threads {
		if accountID != "" && t.AccountID != accountID {
			continue
		}
		if agentID != nil && *agentID != "" && t.AgentID != *agentID {
			continue
		}
		threads = append(threads, t)
	}
	return threads, nil
}

var _ Repository = (*InMemoryRepository)(nil)
