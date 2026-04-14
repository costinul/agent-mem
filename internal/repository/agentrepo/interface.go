package agentrepo

import (
	"context"

	models "agentmem/internal/models"
)

type Repository interface {
	CreateAgent(ctx context.Context, accountID, name string) (*models.Agent, error)
	GetAgentByID(ctx context.Context, accountID, agentID string) (*models.Agent, error)
	DeleteAgentByID(ctx context.Context, accountID, agentID string) (bool, error)
	ListAllAgents(ctx context.Context, accountID string) ([]models.Agent, error)
	UpdateAgent(ctx context.Context, accountID, agentID, name string) error

	CreateThread(ctx context.Context, accountID, agentID string) (*models.Thread, error)
	GetThreadByID(ctx context.Context, accountID, threadID string) (*models.Thread, error)
	DeleteThreadByID(ctx context.Context, accountID, threadID string) (bool, error)
	ListAllThreads(ctx context.Context, accountID string, agentID *string) ([]models.Thread, error)
}
