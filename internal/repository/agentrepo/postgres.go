package agentrepo

import (
	"context"
	"database/sql"
	"fmt"

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

func (r *PostgresRepository) ListAllAgents(ctx context.Context, accountID string) ([]models.Agent, error) {
	query := `SELECT id, account_id, name, created_at, updated_at FROM agents`
	var args []any
	if accountID != "" {
		query += ` WHERE account_id = $1`
		args = append(args, accountID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := make([]models.Agent, 0)
	for rows.Next() {
		var a models.Agent
		if err := rows.Scan(&a.ID, &a.AccountID, &a.Name, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (r *PostgresRepository) UpdateAgent(ctx context.Context, accountID, agentID, name string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE agents SET name = $3, updated_at = now() WHERE id = $1 AND account_id = $2`,
		agentID, accountID, name)
	return err
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

func (r *PostgresRepository) ListAllThreads(ctx context.Context, accountID string, agentID *string) ([]models.Thread, error) {
	query := `SELECT id, account_id, agent_id, created_at FROM threads WHERE 1=1`
	args := make([]any, 0, 2)
	idx := 1
	if accountID != "" {
		query += fmt.Sprintf(` AND account_id = $%d`, idx)
		args = append(args, accountID)
		idx++
	}
	if agentID != nil && *agentID != "" {
		query += fmt.Sprintf(` AND agent_id = $%d`, idx)
		args = append(args, *agentID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	threads := make([]models.Thread, 0)
	for rows.Next() {
		var t models.Thread
		if err := rows.Scan(&t.ID, &t.AccountID, &t.AgentID, &t.CreatedAt); err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}
