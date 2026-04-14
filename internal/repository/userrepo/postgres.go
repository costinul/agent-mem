package userrepo

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

func (r *PostgresRepository) UpsertByGoogleSub(ctx context.Context, params UpsertParams) (*models.User, error) {
	var user models.User
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, picture, google_sub)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (google_sub) DO UPDATE SET
		   name = EXCLUDED.name,
		   picture = EXCLUDED.picture,
		   updated_at = now()
		 RETURNING id, email, name, picture, google_sub, role, created_at, updated_at`,
		params.Email, params.Name, params.Picture, params.GoogleSub,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Picture,
		&user.GoogleSub, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*models.User, error) {
	var user models.User
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, name, picture, google_sub, role, created_at, updated_at
		 FROM users WHERE id = $1`, id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Picture,
		&user.GoogleSub, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *PostgresRepository) ListAll(ctx context.Context) ([]models.User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, email, name, picture, google_sub, role, created_at, updated_at
		 FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Picture,
			&u.GoogleSub, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *PostgresRepository) UpdateRole(ctx context.Context, id, role string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET role = $2, updated_at = now() WHERE id = $1`,
		id, role)
	return err
}

func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}
