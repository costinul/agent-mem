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

var _ Repository = (*InMemoryRepository)(nil)
