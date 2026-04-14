package userrepo

import (
	"context"

	models "agentmem/internal/models"
)

type UpsertParams struct {
	Email     string
	Name      string
	Picture   string
	GoogleSub string
}

type Repository interface {
	UpsertByGoogleSub(ctx context.Context, params UpsertParams) (*models.User, error)
	GetByID(ctx context.Context, id string) (*models.User, error)
	ListAll(ctx context.Context) ([]models.User, error)
	UpdateRole(ctx context.Context, id, role string) error
	Delete(ctx context.Context, id string) error
}
