package memoryrepo

import (
	"context"

	models "agentmem/internal/models"
)

type Repository interface {
	InsertEvent(ctx context.Context, event models.Event) (*models.Event, error)

	InsertSource(ctx context.Context, source models.Source) (*models.Source, error)
	GetSourceByID(ctx context.Context, sourceID string) (*models.Source, error)
	ListSourcesByEventID(ctx context.Context, eventID string) ([]models.Source, error)
	ListConversationSourcesBySessionID(ctx context.Context, sessionID string, limit int) ([]models.Source, error)

	InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error)
	ListFactsByScope(ctx context.Context, accountID string, agentID, sessionID *string) ([]models.Fact, error)
	GetFactByID(ctx context.Context, factID string) (*models.Fact, error)
	SearchFactsByEmbedding(ctx context.Context, params SearchByEmbeddingParams) ([]models.Fact, error)
	UpdateFact(ctx context.Context, fact models.Fact) error
	DeleteFacts(ctx context.Context, factIDs []string) error

}

type SearchByEmbeddingParams struct {
	AccountID     string
	AgentID       *string
	SessionID     *string
	Embedding     []float64
	MinSimilarity float64
	Limit         int
}
