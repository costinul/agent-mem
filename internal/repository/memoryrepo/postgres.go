package memoryrepo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		threadID  sql.NullString
		createdAt time.Time
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO events (account_id, agent_id, thread_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, account_id, agent_id, thread_id, created_at`,
		event.AccountID,
		event.AgentID,
		event.ThreadID,
	).Scan(&stored.ID, &stored.AccountID, &stored.AgentID, &threadID, &createdAt)
	if err != nil {
		return nil, err
	}

	stored.ThreadID = nullStringPtr(threadID)
	stored.CreatedAt = createdAt
	return &stored, nil
}

func (r *PostgresRepository) ListEventsByThreadID(ctx context.Context, threadID string) ([]models.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, account_id, agent_id, thread_id, created_at
		 FROM events WHERE thread_id = $1 ORDER BY created_at ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]models.Event, 0)
	for rows.Next() {
		var e models.Event
		var tid sql.NullString
		if err := rows.Scan(&e.ID, &e.AccountID, &e.AgentID, &tid, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ThreadID = nullStringPtr(tid)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *PostgresRepository) InsertSource(ctx context.Context, source models.Source) (*models.Source, error) {
	var (
		stored     models.Source
		content    sql.NullString
		bucketPath sql.NullString
		sizeBytes  sql.NullInt64
		createdAt  time.Time
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO sources (event_id, kind, content, content_type, bucket_path, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, event_id, kind, content, content_type, bucket_path, size_bytes, created_at`,
		source.EventID,
		source.Kind,
		source.Content,
		source.ContentType,
		source.BucketPath,
		source.SizeBytes,
	).Scan(
		&stored.ID,
		&stored.EventID,
		&stored.Kind,
		&content,
		&stored.ContentType,
		&bucketPath,
		&sizeBytes,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}

	stored.Content = nullStringPtr(content)
	stored.BucketPath = nullStringPtr(bucketPath)
	stored.SizeBytes = nullInt64Ptr(sizeBytes)
	stored.CreatedAt = createdAt
	return &stored, nil
}

func (r *PostgresRepository) GetSourceByID(ctx context.Context, sourceID string) (*models.Source, error) {
	var (
		source     models.Source
		content    sql.NullString
		bucketPath sql.NullString
		sizeBytes  sql.NullInt64
	)

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, event_id, kind, content, content_type, bucket_path, size_bytes, created_at
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
	return &source, nil
}

func (r *PostgresRepository) ListSourcesByEventID(ctx context.Context, eventID string) ([]models.Source, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, event_id, kind, content, content_type, bucket_path, size_bytes, created_at
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
		)
		if err := rows.Scan(
			&source.ID,
			&source.EventID,
			&source.Kind,
			&content,
			&source.ContentType,
			&bucketPath,
			&sizeBytes,
			&source.CreatedAt,
		); err != nil {
			return nil, err
		}
		source.Content = nullStringPtr(content)
		source.BucketPath = nullStringPtr(bucketPath)
		source.SizeBytes = nullInt64Ptr(sizeBytes)
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (r *PostgresRepository) ListConversationSourcesByThreadID(ctx context.Context, threadID string, limit int) ([]models.Source, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT s.id, s.event_id, s.kind, s.content, s.content_type, s.bucket_path, s.size_bytes, s.created_at
		 FROM sources s
		 JOIN events e ON e.id = s.event_id
		 WHERE e.thread_id = $1
		   AND s.kind IN ('USER', 'AGENT')
		 ORDER BY s.created_at DESC
		 LIMIT $2`,
		threadID,
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
		)
		if err := rows.Scan(
			&source.ID,
			&source.EventID,
			&source.Kind,
			&content,
			&source.ContentType,
			&bucketPath,
			&sizeBytes,
			&source.CreatedAt,
		); err != nil {
			return nil, err
		}
		source.Content = nullStringPtr(content)
		source.BucketPath = nullStringPtr(bucketPath)
		source.SizeBytes = nullInt64Ptr(sizeBytes)
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
		stored   models.Fact
		agentID  sql.NullString
		threadID sql.NullString
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO facts (account_id, agent_id, thread_id, source_id, kind, text, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::vector)
		 RETURNING id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at`,
		fact.AccountID,
		fact.AgentID,
		fact.ThreadID,
		fact.SourceID,
		fact.Kind,
		fact.Text,
		vectorParam(fact.Embedding),
	).Scan(
		&stored.ID,
		&stored.AccountID,
		&agentID,
		&threadID,
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
	stored.ThreadID = nullStringPtr(threadID)
	return &stored, nil
}

func (r *PostgresRepository) ListFactsByScope(ctx context.Context, accountID string, agentID, threadID *string) ([]models.Fact, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at
		 FROM facts
		 WHERE account_id = $1
		   AND ($2::uuid IS NULL OR agent_id = $2)
		   AND ($3::uuid IS NULL OR thread_id = $3)
		   AND superseded_at IS NULL
		 ORDER BY created_at ASC`,
		accountID,
		agentID,
		threadID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := make([]models.Fact, 0)
	for rows.Next() {
		var (
			fact   models.Fact
			agent  sql.NullString
			thread sql.NullString
		)
		if err := rows.Scan(
			&fact.ID,
			&fact.AccountID,
			&agent,
			&thread,
			&fact.SourceID,
			&fact.Kind,
			&fact.Text,
			&fact.CreatedAt,
			&fact.UpdatedAt,
		); err != nil {
			return nil, err
		}
		fact.AgentID = nullStringPtr(agent)
		fact.ThreadID = nullStringPtr(thread)
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *PostgresRepository) ListFactsByThreadID(ctx context.Context, threadID string) ([]models.Fact, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text,
		        superseded_at, superseded_by, created_at, updated_at
		 FROM facts WHERE thread_id = $1
		 ORDER BY created_at ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := make([]models.Fact, 0)
	for rows.Next() {
		var (
			fact         models.Fact
			agent        sql.NullString
			thread       sql.NullString
			supersededAt sql.NullTime
			supersededBy sql.NullString
		)
		if err := rows.Scan(
			&fact.ID, &fact.AccountID, &agent, &thread,
			&fact.SourceID, &fact.Kind, &fact.Text,
			&supersededAt, &supersededBy,
			&fact.CreatedAt, &fact.UpdatedAt,
		); err != nil {
			return nil, err
		}
		fact.AgentID = nullStringPtr(agent)
		fact.ThreadID = nullStringPtr(thread)
		if supersededAt.Valid {
			fact.SupersededAt = &supersededAt.Time
		}
		if supersededBy.Valid {
			fact.SupersededBy = &supersededBy.String
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *PostgresRepository) ListFactsFiltered(ctx context.Context, params ListFactsParams) ([]models.Fact, int, error) {
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Offset < 0 {
		params.Offset = 0
	}

	baseWhere := `WHERE account_id = $1
		   AND ($2::uuid IS NULL OR agent_id = $2)
		   AND ($3::uuid IS NULL OR thread_id = $3)
		   AND ($4::text IS NULL OR kind = $4)
		   AND superseded_at IS NULL`

	var kindParam *string
	if params.Kind != nil {
		k := string(*params.Kind)
		kindParam = &k
	}

	var total int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM facts `+baseWhere,
		params.AccountID, params.AgentID, params.ThreadID, kindParam,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at
		 FROM facts `+baseWhere+`
		 ORDER BY created_at DESC
		 LIMIT $5 OFFSET $6`,
		params.AccountID, params.AgentID, params.ThreadID, kindParam, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	facts := make([]models.Fact, 0)
	for rows.Next() {
		var (
			fact   models.Fact
			agent  sql.NullString
			thread sql.NullString
		)
		if err := rows.Scan(
			&fact.ID, &fact.AccountID, &agent, &thread,
			&fact.SourceID, &fact.Kind, &fact.Text,
			&fact.CreatedAt, &fact.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		fact.AgentID = nullStringPtr(agent)
		fact.ThreadID = nullStringPtr(thread)
		facts = append(facts, fact)
	}
	return facts, total, rows.Err()
}

func (r *PostgresRepository) GetFactByID(ctx context.Context, factID string) (*models.Fact, error) {
	var (
		fact     models.Fact
		agentID  sql.NullString
		threadID sql.NullString
	)

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at
		 FROM facts
		 WHERE id = $1`,
		factID,
	).Scan(
		&fact.ID,
		&fact.AccountID,
		&agentID,
		&threadID,
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
	fact.ThreadID = nullStringPtr(threadID)
	return &fact, nil
}

func (r *PostgresRepository) UpdateFact(ctx context.Context, fact models.Fact) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE facts
		 SET text = $2, kind = $3, embedding = COALESCE($4::vector, embedding), updated_at = now()
		 WHERE id = $1`,
		fact.ID,
		fact.Text,
		fact.Kind,
		vectorParam(fact.Embedding),
	)
	return err
}

func (r *PostgresRepository) SearchFactsByEmbedding(ctx context.Context, params SearchByEmbeddingParams) ([]models.Fact, error) {
	if len(params.Embedding) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.MinSimilarity <= 0 {
		params.MinSimilarity = 0.65
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at
		 FROM facts
		 WHERE account_id = $1
		   AND ($2::uuid IS NULL OR agent_id = $2)
		   AND ($3::uuid IS NULL OR thread_id = $3)
		   AND embedding IS NOT NULL
		   AND superseded_at IS NULL
		   AND (1 - (embedding <=> $4::vector)) >= $5
		 ORDER BY embedding <=> $4::vector ASC
		 LIMIT $6`,
		params.AccountID,
		params.AgentID,
		params.ThreadID,
		vectorLiteral(params.Embedding),
		params.MinSimilarity,
		params.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := make([]models.Fact, 0)
	for rows.Next() {
		var (
			fact   models.Fact
			agent  sql.NullString
			thread sql.NullString
		)
		if err := rows.Scan(
			&fact.ID,
			&fact.AccountID,
			&agent,
			&thread,
			&fact.SourceID,
			&fact.Kind,
			&fact.Text,
			&fact.CreatedAt,
			&fact.UpdatedAt,
		); err != nil {
			return nil, err
		}
		fact.AgentID = nullStringPtr(agent)
		fact.ThreadID = nullStringPtr(thread)
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *PostgresRepository) SearchFactsByEmbeddingWithScores(ctx context.Context, params SearchByEmbeddingParams) ([]FactWithScore, error) {
	if len(params.Embedding) == 0 {
		return nil, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, account_id, agent_id, thread_id, source_id, kind, text, created_at, updated_at,
		        1 - (embedding <=> $4::vector) AS score
		 FROM facts
		 WHERE account_id = $1
		   AND ($2::uuid IS NULL OR agent_id = $2)
		   AND ($3::uuid IS NULL OR thread_id = $3)
		   AND embedding IS NOT NULL
		   AND superseded_at IS NULL
		 ORDER BY embedding <=> $4::vector ASC
		 LIMIT $5`,
		params.AccountID,
		params.AgentID,
		params.ThreadID,
		vectorLiteral(params.Embedding),
		params.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]FactWithScore, 0)
	for rows.Next() {
		var (
			fs     FactWithScore
			agent  sql.NullString
			thread sql.NullString
		)
		if err := rows.Scan(
			&fs.ID,
			&fs.AccountID,
			&agent,
			&thread,
			&fs.SourceID,
			&fs.Kind,
			&fs.Text,
			&fs.CreatedAt,
			&fs.UpdatedAt,
			&fs.Score,
		); err != nil {
			return nil, err
		}
		fs.AgentID = nullStringPtr(agent)
		fs.ThreadID = nullStringPtr(thread)
		results = append(results, fs)
	}
	return results, rows.Err()
}

func (r *PostgresRepository) DeleteFact(ctx context.Context, factID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM facts WHERE id = $1`, factID)
	return err
}

func (r *PostgresRepository) SupersedeFact(ctx context.Context, oldFactID string, newFact models.Fact) (*models.Fact, error) {
	inserted, err := r.InsertFact(ctx, newFact)
	if err != nil {
		return nil, fmt.Errorf("insert successor fact: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE facts SET superseded_at = now(), superseded_by = $2, updated_at = now() WHERE id = $1`,
		oldFactID, inserted.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("supersede fact %s: %w", oldFactID, err)
	}
	return inserted, nil
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

func vectorLiteral(values []float64) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%g", value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func vectorParam(values []float64) any {
	if len(values) == 0 {
		return nil
	}
	return vectorLiteral(values)
}
