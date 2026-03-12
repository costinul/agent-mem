package memoryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"agentmem/internal/database"
	models "agentmem/internal/models"
)

type PostgresRepository struct {
	db *database.DB
}

func NewPostgres(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) InsertEvent(ctx context.Context, event models.Event) (*models.Event, error) {
	var (
		stored    models.Event
		sessionID sql.NullString
		createdAt time.Time
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO events (account_id, agent_id, session_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, account_id, agent_id, session_id, created_at`,
		event.AccountID,
		event.AgentID,
		event.SessionID,
	).Scan(&stored.ID, &stored.AccountID, &stored.AgentID, &sessionID, &createdAt)
	if err != nil {
		return nil, err
	}

	stored.SessionID = nullStringPtr(sessionID)
	stored.CreatedAt = createdAt
	return &stored, nil
}

func (r *PostgresRepository) InsertSource(ctx context.Context, source models.Source) (*models.Source, error) {
	var (
		stored     models.Source
		content    sql.NullString
		bucketPath sql.NullString
		sizeBytes  sql.NullInt64
		metadata   []byte
		createdAt  time.Time
	)

	metadataJSON, err := json.Marshal(source.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	err = r.db.QueryRowContext(
		ctx,
		`INSERT INTO sources (event_id, kind, content, content_type, bucket_path, size_bytes, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, event_id, kind, content, content_type, bucket_path, size_bytes, metadata, created_at`,
		source.EventID,
		source.Kind,
		source.Content,
		source.ContentType,
		source.BucketPath,
		source.SizeBytes,
		metadataJSON,
	).Scan(
		&stored.ID,
		&stored.EventID,
		&stored.Kind,
		&content,
		&stored.ContentType,
		&bucketPath,
		&sizeBytes,
		&metadata,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}

	stored.Content = nullStringPtr(content)
	stored.BucketPath = nullStringPtr(bucketPath)
	stored.SizeBytes = nullInt64Ptr(sizeBytes)
	stored.CreatedAt = createdAt

	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &stored.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &stored, nil
}

func (r *PostgresRepository) GetSourceByID(ctx context.Context, sourceID string) (*models.Source, error) {
	var (
		source     models.Source
		content    sql.NullString
		bucketPath sql.NullString
		sizeBytes  sql.NullInt64
		metadata   []byte
	)

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, event_id, kind, content, content_type, bucket_path, size_bytes, metadata, created_at
		 FROM sources
		 WHERE id = $1`,
		sourceID,
	).Scan(
		&source.ID,
		&source.EventID,
		&source.Kind,
		&content,
		&source.ContentType,
		&bucketPath,
		&sizeBytes,
		&metadata,
		&source.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	source.Content = nullStringPtr(content)
	source.BucketPath = nullStringPtr(bucketPath)
	source.SizeBytes = nullInt64Ptr(sizeBytes)

	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &source.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &source, nil
}

func (r *PostgresRepository) ListSourcesByEventID(ctx context.Context, eventID string) ([]models.Source, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, event_id, kind, content, content_type, bucket_path, size_bytes, metadata, created_at
		 FROM sources
		 WHERE event_id = $1
		 ORDER BY created_at ASC`,
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := make([]models.Source, 0)
	for rows.Next() {
		var (
			source     models.Source
			content    sql.NullString
			bucketPath sql.NullString
			sizeBytes  sql.NullInt64
			metadata   []byte
		)
		if err := rows.Scan(
			&source.ID,
			&source.EventID,
			&source.Kind,
			&content,
			&source.ContentType,
			&bucketPath,
			&sizeBytes,
			&metadata,
			&source.CreatedAt,
		); err != nil {
			return nil, err
		}
		source.Content = nullStringPtr(content)
		source.BucketPath = nullStringPtr(bucketPath)
		source.SizeBytes = nullInt64Ptr(sizeBytes)
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &source.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (r *PostgresRepository) ListConversationSourcesBySessionID(ctx context.Context, sessionID string, limit int) ([]models.Source, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT s.id, s.event_id, s.kind, s.content, s.content_type, s.bucket_path, s.size_bytes, s.metadata, s.created_at
		 FROM sources s
		 JOIN events e ON e.id = s.event_id
		 WHERE e.session_id = $1
		   AND s.kind IN ('USER', 'AGENT')
		 ORDER BY s.created_at DESC
		 LIMIT $2`,
		sessionID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := make([]models.Source, 0)
	for rows.Next() {
		var (
			source     models.Source
			content    sql.NullString
			bucketPath sql.NullString
			sizeBytes  sql.NullInt64
			metadata   []byte
		)
		if err := rows.Scan(
			&source.ID,
			&source.EventID,
			&source.Kind,
			&content,
			&source.ContentType,
			&bucketPath,
			&sizeBytes,
			&metadata,
			&source.CreatedAt,
		); err != nil {
			return nil, err
		}
		source.Content = nullStringPtr(content)
		source.BucketPath = nullStringPtr(bucketPath)
		source.SizeBytes = nullInt64Ptr(sizeBytes)
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &source.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(sources)-1; i < j; i, j = i+1, j-1 {
		sources[i], sources[j] = sources[j], sources[i]
	}

	return sources, nil
}

func (r *PostgresRepository) InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error) {
	var (
		stored    models.Fact
		agentID   sql.NullString
		sessionID sql.NullString
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO facts (account_id, agent_id, session_id, source_id, kind, text, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, NULL)
		 RETURNING id, account_id, agent_id, session_id, source_id, kind, text, created_at, updated_at`,
		fact.AccountID,
		fact.AgentID,
		fact.SessionID,
		fact.SourceID,
		fact.Kind,
		fact.Text,
	).Scan(
		&stored.ID,
		&stored.AccountID,
		&agentID,
		&sessionID,
		&stored.SourceID,
		&stored.Kind,
		&stored.Text,
		&stored.CreatedAt,
		&stored.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	stored.AgentID = nullStringPtr(agentID)
	stored.SessionID = nullStringPtr(sessionID)
	return &stored, nil
}

func (r *PostgresRepository) GetFactByID(ctx context.Context, factID string) (*models.Fact, error) {
	var (
		fact      models.Fact
		agentID   sql.NullString
		sessionID sql.NullString
	)

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, account_id, agent_id, session_id, source_id, kind, text, created_at, updated_at
		 FROM facts
		 WHERE id = $1`,
		factID,
	).Scan(
		&fact.ID,
		&fact.AccountID,
		&agentID,
		&sessionID,
		&fact.SourceID,
		&fact.Kind,
		&fact.Text,
		&fact.CreatedAt,
		&fact.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	fact.AgentID = nullStringPtr(agentID)
	fact.SessionID = nullStringPtr(sessionID)
	return &fact, nil
}

func (r *PostgresRepository) UpdateFact(ctx context.Context, fact models.Fact) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE facts
		 SET text = $2, kind = $3, updated_at = now()
		 WHERE id = $1`,
		fact.ID,
		fact.Text,
		fact.Kind,
	)
	return err
}

func (r *PostgresRepository) DeleteFacts(ctx context.Context, factIDs []string) error {
	if len(factIDs) == 0 {
		return nil
	}
	for _, factID := range factIDs {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM facts WHERE id = $1`, factID); err != nil {
			return fmt.Errorf("delete fact %s: %w", factID, err)
		}
	}
	return nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	typed := value.String
	return &typed
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	typed := value.Int64
	return &typed
}
