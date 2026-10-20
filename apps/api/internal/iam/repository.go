package iam

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserRepository defines the interface for user persistence.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
}

// postgresUserRepository is a PostgreSQL-backed user repository.
type postgresUserRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresUserRepository creates a PostgreSQL user repository.
func NewPostgresUserRepository(pool *pgxpool.Pool) UserRepository {
	return &postgresUserRepository{pool: pool}
}

func (r *postgresUserRepository) Create(ctx context.Context, user *User) error {
	query := `
		INSERT INTO users (id, workspace_id, email, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.pool.Exec(ctx, query, uuid.New().String(), user.WorkspaceID, user.Email, user.PasswordHash, user.Role)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (r *postgresUserRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, workspace_id, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE email = $1
	`
	user := &User{}
	err := r.pool.QueryRow(ctx, query, email).Scan(
		&user.ID, &user.WorkspaceID, &user.Email, &user.PasswordHash,
		&user.Role, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return user, nil
}

func (r *postgresUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	query := `
		SELECT id, workspace_id, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE id = $1
	`
	user := &User{}
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.WorkspaceID, &user.Email, &user.PasswordHash,
		&user.Role, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return user, nil
}
