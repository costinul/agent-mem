package memoryrepo

import (
	"context"

	models "agentmem/internal/models"
)

type Repository interface {
	InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error)
	GetFactByID(ctx context.Context, factID string) (*models.Fact, error)
	UpdateFact(ctx context.Context, fact models.Fact) error
	DeleteFacts(ctx context.Context, factIDs []string) error

	InsertFactLink(ctx context.Context, link models.FactLink) (*models.FactLink, error)
	ListFactLinksByFactID(ctx context.Context, factID string) ([]models.FactLink, error)

	InsertRawMessage(ctx context.Context, msg models.RawMessage) (*models.RawMessage, error)
	ListRawMessagesBySessionID(ctx context.Context, sessionID string, limit int) ([]models.RawMessage, error)
}
