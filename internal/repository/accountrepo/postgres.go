package accountrepo

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

func (r *PostgresRepository) CreateAccount(ctx context.Context, name string) (*models.Account, error) {
	var account models.Account
	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO accounts (name) VALUES ($1) RETURNING id, name, created_at, updated_at`,
		name,
	).Scan(&account.ID, &account.Name, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return &account, nil
}

func (r *PostgresRepository) CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (*models.APIKey, error) {
	var normalizedLabel sql.NullString
	if params.Label != nil {
		trimmed := strings.TrimSpace(*params.Label)
		if trimmed != "" {
			normalizedLabel = sql.NullString{String: trimmed, Valid: true}
		}
	}

	var (
		stored      models.APIKey
		dbLabel     sql.NullString
		dbExpiresAt sql.NullTime
	)

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO api_keys (account_id, prefix, key_hash, label, expires_at, valid)
		 VALUES ($1, $2, $3, $4, $5, true)
		 RETURNING id, account_id, prefix, key_hash, label, expires_at, valid, created_at`,
		params.AccountID,
		params.Prefix,
		params.KeyHash,
		normalizedLabel,
		params.ExpiresAt,
	).Scan(
		&stored.ID,
		&stored.AccountID,
		&stored.Prefix,
		&stored.KeyHash,
		&dbLabel,
		&dbExpiresAt,
		&stored.Valid,
		&stored.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	stored.Label = nullStringPtr(dbLabel)
	stored.ExpiresAt = nullTimePtr(dbExpiresAt)
	return &stored, nil
}

func (r *PostgresRepository) InvalidateAPIKeyByID(ctx context.Context, apiKeyID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE api_keys SET valid = false WHERE id = $1`, apiKeyID)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *PostgresRepository) InvalidateAPIKeyByPrefix(ctx context.Context, prefix string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE api_keys SET valid = false WHERE prefix = $1`, prefix)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *PostgresRepository) ListAPIKeysByPrefix(ctx context.Context, prefix string) ([]models.APIKey, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, account_id, prefix, key_hash, label, expires_at, valid, created_at
		 FROM api_keys
		 WHERE prefix = $1`,
		prefix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]models.APIKey, 0)
	for rows.Next() {
		key, scanErr := scanAPIKey(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		keys = append(keys, *key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func scanAPIKey(scanner interface {
	Scan(dest ...any) error
}) (*models.APIKey, error) {
	var (
		key       models.APIKey
		label     sql.NullString
		expiresAt sql.NullTime
	)

	err := scanner.Scan(
		&key.ID,
		&key.AccountID,
		&key.Prefix,
		&key.KeyHash,
		&label,
		&expiresAt,
		&key.Valid,
		&key.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	key.Label = nullStringPtr(label)
	key.ExpiresAt = nullTimePtr(expiresAt)
	return &key, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	typed := value.String
	return &typed
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	typed := value.Time
	return &typed
}
