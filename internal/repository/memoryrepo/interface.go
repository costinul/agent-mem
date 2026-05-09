package memoryrepo

import (
	"context"
	"time"

	models "agentmem/internal/models"
)

type Repository interface {
	InsertEvent(ctx context.Context, event models.Event) (*models.Event, error)
	ListEventsByThreadID(ctx context.Context, threadID string) ([]models.Event, error)

	InsertSource(ctx context.Context, source models.Source) (*models.Source, error)
	GetSourceByID(ctx context.Context, sourceID string) (*models.Source, error)
	ListSourcesByEventID(ctx context.Context, eventID string) ([]models.Source, error)
	ListConversationSourcesByThreadID(ctx context.Context, threadID string, limit int) ([]models.Source, error)
	// SearchSourcesByContent finds sources whose content starts with text, scoped by account/agent/thread.
	// agentID and threadID are optional (empty string = no filter).
	SearchSourcesByContent(ctx context.Context, accountID, agentID, threadID, text string) ([]models.Source, error)

	InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error)
	ListFactsByScope(ctx context.Context, accountID string, agentID, threadID *string) ([]models.Fact, error)
	ListFactsByThreadID(ctx context.Context, threadID string) ([]models.Fact, error)
	ListFactsBySourceIDs(ctx context.Context, accountID string, sourceIDs []string) ([]models.Fact, error)
	ListFactsFiltered(ctx context.Context, params ListFactsParams) ([]models.Fact, int, error)
	GetFactByID(ctx context.Context, factID string) (*models.Fact, error)
	SearchFactsByEmbedding(ctx context.Context, params SearchByEmbeddingParams) ([]models.Fact, error)
	SearchFactsByEmbeddingWithScores(ctx context.Context, params SearchByEmbeddingParams) ([]FactWithScore, error)
	UpdateFact(ctx context.Context, fact models.Fact) error
	DeleteFact(ctx context.Context, factID string) error
	// SupersedeFact inserts newFact and marks oldFactID as superseded.
	// supersededAt is the boundary timestamp written to the old fact's superseded_at:
	// the old fact is "active at time T" iff supersededAt > T. Callers should pass the
	// successor's content boundary (typically the successor's referenced_at, falling back
	// to its source event_date) so as-of recall can reconstruct the timeline.
	SupersedeFact(ctx context.Context, oldFactID string, newFact models.Fact, supersededAt time.Time) (*models.Fact, error)

	// MaxSourceEventDateForThread returns the most recent event_date across all sources for the thread.
	// Returns nil when the thread has no sources.
	MaxSourceEventDateForThread(ctx context.Context, threadID string) (*time.Time, error)
}

type ListFactsParams struct {
	AccountID string
	AgentID   *string
	ThreadID  *string
	Kind      *models.FactKind
	Limit     int
	Offset    int
}

type FactWithScore struct {
	models.Fact
	Score float64
}

type SearchByEmbeddingParams struct {
	AccountID     string
	AgentID       *string
	ThreadID      *string
	Embedding     []float64
	MinSimilarity float64
	Limit         int
	SourceIDs     []string // optional; when non-empty, restricts scoring to facts from these sources
	// IncludeSuperseded keeps superseded facts in the result set. Recall sets this so the
	// HISTORICAL marker can be evaluated relative to the recall event_date instead of being
	// hard-filtered at SQL time. The ingest pipeline keeps it false to compare new facts
	// only against the current state.
	IncludeSuperseded bool
}
