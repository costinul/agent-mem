package agentrepo

import (
	"context"
	"database/sql"
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

func (r *PostgresRepository) CreateAgent(ctx context.Context, accountID, name string) (*models.Agent, error) {
	var agent models.Agent
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO agents (account_id, name)
		 VALUES ($1, $2)
		 RETURNING id, account_id, name, created_at, updated_at`,
		accountID,
		name,
	).Scan(&agent.ID, &agent.AccountID, &agent.Name, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

func (r *PostgresRepository) GetAgentByID(ctx context.Context, accountID, agentID string) (*models.Agent, error) {
	var agent models.Agent
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, account_id, name, created_at, updated_at
		 FROM agents
		 WHERE id = $1 AND account_id = $2`,
		agentID,
		accountID,
	).Scan(&agent.ID, &agent.AccountID, &agent.Name, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &agent, nil
}

func (r *PostgresRepository) DeleteAgentByID(ctx context.Context, accountID, agentID string) (bool, error) {
	result, err := r.db.ExecContext(
		ctx,
		`DELETE FROM agents
		 WHERE id = $1 AND account_id = $2`,
		agentID,
		accountID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, accountID, agentID string) (*models.Session, error) {
	var (
		session  models.Session
		closedAt sql.NullTime
	)
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO sessions (account_id, agent_id)
		 VALUES ($1, $2)
		 RETURNING id, account_id, agent_id, created_at, closed_at`,
		accountID,
		agentID,
	).Scan(&session.ID, &session.AccountID, &session.AgentID, &session.CreatedAt, &closedAt)
	if err != nil {
		return nil, err
	}
	session.ClosedAt = nullTimePtr(closedAt)
	return &session, nil
}

func (r *PostgresRepository) GetSessionByID(ctx context.Context, accountID, agentID, sessionID string) (*models.Session, error) {
	var (
		session  models.Session
		closedAt sql.NullTime
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, account_id, agent_id, created_at, closed_at
		 FROM sessions
		 WHERE id = $1 AND account_id = $2 AND agent_id = $3`,
		sessionID,
		accountID,
		agentID,
	).Scan(&session.ID, &session.AccountID, &session.AgentID, &session.CreatedAt, &closedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	session.ClosedAt = nullTimePtr(closedAt)
	return &session, nil
}

func (r *PostgresRepository) CloseSessionByID(ctx context.Context, accountID, agentID, sessionID string) (bool, error) {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE sessions
		 SET closed_at = now()
		 WHERE id = $1
		   AND account_id = $2
		   AND agent_id = $3
		   AND closed_at IS NULL`,
		sessionID,
		accountID,
		agentID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	typed := value.Time
	return &typed
}
