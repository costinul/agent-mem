package accountrepo

import (
	"context"
	"time"

	models "agentmem/internal/models"
)

type CreateAPIKeyParams struct {
	AccountID string
	Prefix    string
	KeyHash   string
	Label     *string
	ExpiresAt *time.Time
}

type Repository interface {
	CreateAccount(ctx context.Context, name string) (*models.Account, error)
	CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (*models.APIKey, error)
	InvalidateAPIKeyByID(ctx context.Context, apiKeyID string) (bool, error)
	InvalidateAPIKeyByPrefix(ctx context.Context, prefix string) (bool, error)
	ListAPIKeysByPrefix(ctx context.Context, prefix string) ([]models.APIKey, error)
}
