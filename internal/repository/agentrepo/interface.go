package agentrepo

import (
	"context"

	models "agentmem/internal/models"
)

type Repository interface {
	CreateAgent(ctx context.Context, accountID, name string) (*models.Agent, error)
	GetAgentByID(ctx context.Context, accountID, agentID string) (*models.Agent, error)
	DeleteAgentByID(ctx context.Context, accountID, agentID string) (bool, error)

	CreateSession(ctx context.Context, accountID, agentID string) (*models.Session, error)
	GetSessionByID(ctx context.Context, accountID, agentID, sessionID string) (*models.Session, error)
	CloseSessionByID(ctx context.Context, accountID, agentID, sessionID string) (bool, error)
}
