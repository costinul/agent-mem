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
	ListConversationSourcesByThreadID(ctx context.Context, threadID string, limit int) ([]models.Source, error)

	InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error)
	ListFactsByScope(ctx context.Context, accountID string, agentID, threadID *string) ([]models.Fact, error)
	ListFactsFiltered(ctx context.Context, params ListFactsParams) ([]models.Fact, int, error)
	GetFactByID(ctx context.Context, factID string) (*models.Fact, error)
	SearchFactsByEmbedding(ctx context.Context, params SearchByEmbeddingParams) ([]models.Fact, error)
	UpdateFact(ctx context.Context, fact models.Fact) error
	DeleteFact(ctx context.Context, factID string) error
	SupersedeFact(ctx context.Context, oldFactID string, newFact models.Fact) (*models.Fact, error)
}

type ListFactsParams struct {
	AccountID string
	AgentID   *string
	ThreadID  *string
	Kind      *models.FactKind
	Limit     int
	Offset    int
}

type SearchByEmbeddingParams struct {
	AccountID     string
	AgentID       *string
	ThreadID      *string
	Embedding     []float64
	MinSimilarity float64
	Limit         int
}
