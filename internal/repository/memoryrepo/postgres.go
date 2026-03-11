package memoryrepo

import (
	"context"
	"database/sql"
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

func (r *PostgresRepository) InsertFact(ctx context.Context, fact models.Fact) (*models.Fact, error) {
	var (
		stored    models.Fact
		agentID   sql.NullString
		sessionID sql.NullString
		createdAt time.Time
		updatedAt time.Time
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO facts (account_id, agent_id, session_id, event_id, source, kind, text, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 RETURNING id, account_id, agent_id, session_id, event_id, source, kind, text, created_at, updated_at`,
		fact.AccountID,
		fact.AgentID,
		fact.SessionID,
		fact.EventID,
		fact.Source,
		fact.Kind,
		fact.Text,
	).Scan(
		&stored.ID,
		&stored.AccountID,
		&agentID,
		&sessionID,
		&stored.EventID,
		&stored.Source,
		&stored.Kind,
		&stored.Text,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	stored.AgentID = nullStringPtr(agentID)
	stored.SessionID = nullStringPtr(sessionID)
	stored.CreatedAt = createdAt
	stored.UpdatedAt = updatedAt
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
		`SELECT id, account_id, agent_id, session_id, event_id, source, kind, text, created_at, updated_at
		 FROM facts
		 WHERE id = $1`,
		factID,
	).Scan(
		&fact.ID,
		&fact.AccountID,
		&agentID,
		&sessionID,
		&fact.EventID,
		&fact.Source,
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
		 SET text = $2, source = $3, kind = $4, updated_at = now()
		 WHERE id = $1`,
		fact.ID,
		fact.Text,
		fact.Source,
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

func (r *PostgresRepository) InsertFactLink(ctx context.Context, link models.FactLink) (*models.FactLink, error) {
	var (
		stored     models.FactLink
		bucketPath sql.NullString
	)
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO fact_links (fact_id, event_id, input_hash, bucket_path)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, fact_id, event_id, input_hash, bucket_path`,
		link.FactID,
		link.EventID,
		link.InputHash,
		link.BucketPath,
	).Scan(&stored.ID, &stored.FactID, &stored.EventID, &stored.InputHash, &bucketPath)
	if err != nil {
		return nil, err
	}
	stored.BucketPath = nullStringPtr(bucketPath)
	return &stored, nil
}

func (r *PostgresRepository) ListFactLinksByFactID(ctx context.Context, factID string) ([]models.FactLink, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, fact_id, event_id, input_hash, bucket_path
		 FROM fact_links
		 WHERE fact_id = $1`,
		factID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := make([]models.FactLink, 0)
	for rows.Next() {
		var (
			link       models.FactLink
			bucketPath sql.NullString
		)
		if err := rows.Scan(&link.ID, &link.FactID, &link.EventID, &link.InputHash, &bucketPath); err != nil {
			return nil, err
		}
		link.BucketPath = nullStringPtr(bucketPath)
		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return links, nil
}

func (r *PostgresRepository) InsertRawMessage(ctx context.Context, msg models.RawMessage) (*models.RawMessage, error) {
	var stored models.RawMessage
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO raw_messages (session_id, event_id, source, content, sequence)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, session_id, event_id, source, content, sequence, created_at`,
		msg.SessionID,
		msg.EventID,
		msg.Source,
		msg.Content,
		msg.Sequence,
	).Scan(
		&stored.ID,
		&stored.SessionID,
		&stored.EventID,
		&stored.Source,
		&stored.Content,
		&stored.Sequence,
		&stored.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *PostgresRepository) ListRawMessagesBySessionID(ctx context.Context, sessionID string, limit int) ([]models.RawMessage, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, session_id, event_id, source, content, sequence, created_at
		 FROM raw_messages
		 WHERE session_id = $1
		 ORDER BY sequence DESC
		 LIMIT $2`,
		sessionID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]models.RawMessage, 0)
	for rows.Next() {
		var msg models.RawMessage
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.EventID, &msg.Source, &msg.Content, &msg.Sequence, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Return in ascending sequence for caller convenience.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	typed := value.String
	return &typed
}
