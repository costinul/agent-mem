package agentrepo

import (
	"context"
	"database/sql"

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

func (r *PostgresRepository) CreateThread(ctx context.Context, accountID, agentID string) (*models.Thread, error) {
	var thread models.Thread
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO threads (account_id, agent_id)
		 VALUES ($1, $2)
		 RETURNING id, account_id, agent_id, created_at`,
		accountID,
		agentID,
	).Scan(&thread.ID, &thread.AccountID, &thread.AgentID, &thread.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &thread, nil
}

func (r *PostgresRepository) GetThreadByID(ctx context.Context, accountID, threadID string) (*models.Thread, error) {
	var thread models.Thread
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, account_id, agent_id, created_at
		 FROM threads
		 WHERE id = $1 AND account_id = $2`,
		threadID,
		accountID,
	).Scan(&thread.ID, &thread.AccountID, &thread.AgentID, &thread.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &thread, nil
}

func (r *PostgresRepository) DeleteThreadByID(ctx context.Context, accountID, threadID string) (bool, error) {
	result, err := r.db.ExecContext(
		ctx,
		`DELETE FROM threads
		 WHERE id = $1
		   AND account_id = $2`,
		threadID,
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
