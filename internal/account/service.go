package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	models "agentmem/internal/models"
	"agentmem/internal/repository/accountrepo"
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
	repo accountrepo.Repository
}

func NewService(repo accountrepo.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateAccount(ctx context.Context, name string) (*models.Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("account name is required")
	}

	account, err := s.repo.CreateAccount(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}

	return account, nil
}

func (s *Service) ListAPIKeysByAccountID(ctx context.Context, accountID string) ([]models.APIKey, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	return s.repo.ListAPIKeysByAccountID(ctx, accountID)
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
	stored, err := s.repo.CreateAPIKey(ctx, accountrepo.CreateAPIKeyParams{
		AccountID: accountID,
		Prefix:    prefix,
		KeyHash:   keyHash,
		Label:     label,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, "", fmt.Errorf("create api key: %w", err)
	}

	return stored, plaintextKey, nil
}

func (s *Service) InvalidateAPIKeyByID(ctx context.Context, apiKeyID string) error {
	apiKeyID = strings.TrimSpace(apiKeyID)
	if apiKeyID == "" {
		return fmt.Errorf("api key id is required")
	}

	updated, err := s.repo.InvalidateAPIKeyByID(ctx, apiKeyID)
	if err != nil {
		return fmt.Errorf("invalidate api key by id: %w", err)
	}
	if !updated {
		return ErrAPIKeyNotFound
	}

	return nil
}

func (s *Service) InvalidateAPIKeyByPrefix(ctx context.Context, prefix string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return fmt.Errorf("api key prefix is required")
	}

	updated, err := s.repo.InvalidateAPIKeyByPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("invalidate api key by prefix: %w", err)
	}
	if !updated {
		return ErrAPIKeyNotFound
	}

	return nil
}

func (s *Service) GetAndValidateKey(ctx context.Context, fullKey string) (*models.APIKey, error) {
	prefix, err := prefixFromAPIKey(fullKey)
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	keys, err := s.repo.ListAPIKeysByPrefix(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("get api key by prefix: %w", err)
	}

	now := time.Now().UTC()
	for i := range keys {
		key := &keys[i]
		if !isAPIKeyUsable(key, now) {
			continue
		}
		if !matchesAPIKey(key.KeyHash, fullKey) {
			continue
		}

		return key, nil
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
