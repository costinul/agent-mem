package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentmem/internal/database"
	models "agentmem/internal/models"
)

const (
	apiKeyPrefixBytes = 6
	apiKeySecretBytes = 24
	apiKeyFormatTag   = "amk"
)

var (
	ErrInvalidAPIKey  = errors.New("invalid api key")
	ErrAPIKeyNotFound = errors.New("api key not found")
)

type Service struct {
	db *database.DB
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateAccount(ctx context.Context, name string) (*models.Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("account name is required")
	}

	var account models.Account
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO accounts (name) VALUES ($1) RETURNING id, name, created_at, updated_at`,
		name,
	).Scan(&account.ID, &account.Name, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}

	return &account, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, accountID string, label *string, expiresAt *time.Time) (*models.APIKey, string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, "", fmt.Errorf("account id is required")
	}

	plaintextKey, prefix, err := generateAPIKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate api key: %w", err)
	}

	keyHash := hashAPIKey(plaintextKey)
	var normalizedLabel sql.NullString
	if label != nil {
		trimmed := strings.TrimSpace(*label)
		if trimmed != "" {
			normalizedLabel = sql.NullString{String: trimmed, Valid: true}
		}
	}

	var (
		stored      models.APIKey
		dbLabel     sql.NullString
		dbExpiresAt sql.NullTime
	)

	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO api_keys (account_id, prefix, key_hash, label, expires_at, valid)
		 VALUES ($1, $2, $3, $4, $5, true)
		 RETURNING id, account_id, prefix, key_hash, label, expires_at, valid, created_at`,
		accountID,
		prefix,
		keyHash,
		normalizedLabel,
		expiresAt,
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
		return nil, "", fmt.Errorf("create api key: %w", err)
	}

	stored.Label = nullStringPtr(dbLabel)
	stored.ExpiresAt = nullTimePtr(dbExpiresAt)

	return &stored, plaintextKey, nil
}

func (s *Service) InvalidateAPIKeyByID(ctx context.Context, apiKeyID string) error {
	apiKeyID = strings.TrimSpace(apiKeyID)
	if apiKeyID == "" {
		return fmt.Errorf("api key id is required")
	}

	result, err := s.db.ExecContext(
		ctx,
		`UPDATE api_keys SET valid = false WHERE id = $1`,
		apiKeyID,
	)
	if err != nil {
		return fmt.Errorf("invalidate api key by id: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("invalidate api key by id rows affected: %w", err)
	}
	if affected == 0 {
		return ErrAPIKeyNotFound
	}

	return nil
}

func (s *Service) InvalidateAPIKeyByPrefix(ctx context.Context, prefix string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return fmt.Errorf("api key prefix is required")
	}

	result, err := s.db.ExecContext(
		ctx,
		`UPDATE api_keys SET valid = false WHERE prefix = $1`,
		prefix,
	)
	if err != nil {
		return fmt.Errorf("invalidate api key by prefix: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("invalidate api key by prefix rows affected: %w", err)
	}
	if affected == 0 {
		return ErrAPIKeyNotFound
	}

	return nil
}

func (s *Service) GetAndValidateKey(ctx context.Context, fullKey string) (*models.APIKey, error) {
	prefix, err := prefixFromAPIKey(fullKey)
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, account_id, prefix, key_hash, label, expires_at, valid, created_at
		 FROM api_keys
		 WHERE prefix = $1`,
		prefix,
	)
	if err != nil {
		return nil, fmt.Errorf("get api key by prefix: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		key, scanErr := scanAPIKey(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan api key: %w", scanErr)
		}

		if !isAPIKeyUsable(key, now) {
			continue
		}
		if !matchesAPIKey(key.KeyHash, fullKey) {
			continue
		}

		return key, nil
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}

	return nil, ErrInvalidAPIKey
}

func generateAPIKey() (string, string, error) {
	prefixBytes := make([]byte, apiKeyPrefixBytes)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", err
	}

	secretBytes := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", err
	}

	prefix := hex.EncodeToString(prefixBytes)
	secret := hex.EncodeToString(secretBytes)
	full := fmt.Sprintf("%s_%s_%s", apiKeyFormatTag, prefix, secret)
	return full, prefix, nil
}

func hashAPIKey(fullKey string) string {
	sum := sha256.Sum256([]byte(fullKey))
	return hex.EncodeToString(sum[:])
}

func matchesAPIKey(storedHash, fullKey string) bool {
	return storedHash == hashAPIKey(fullKey)
}

func prefixFromAPIKey(fullKey string) (string, error) {
	parts := strings.Split(fullKey, "_")
	if len(parts) != 3 || parts[0] != apiKeyFormatTag || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("invalid api key format")
	}

	return parts[1], nil
}

func isAPIKeyUsable(key *models.APIKey, now time.Time) bool {
	if !key.Valid {
		return false
	}

	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return false
	}

	return true
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
