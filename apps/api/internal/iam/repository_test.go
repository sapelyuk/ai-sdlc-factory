package iam

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// fakePool records the executed statements and returns canned results.
type fakePool struct {
	execSQL  string
	execArgs []any
	execTag  pgconn.CommandTag
	execErr  error

	querySQL  string
	queryArgs []any
	row       pgx.Row
}

func (p *fakePool) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	p.execSQL, p.execArgs = sql, arguments
	return p.execTag, p.execErr
}

func (p *fakePool) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	p.querySQL, p.queryArgs = sql, args
	return p.row
}

// fakeRow fills scan destinations with predefined values.
type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan: got %d destinations, want %d", len(dest), len(r.values))
	}
	for i, d := range dest {
		switch target := d.(type) {
		case *string:
			value, ok := r.values[i].(string)
			if !ok {
				return fmt.Errorf("scan: value %d is %T, want string", i, r.values[i])
			}
			*target = value
		case *time.Time:
			value, ok := r.values[i].(time.Time)
			if !ok {
				return fmt.Errorf("scan: value %d is %T, want time.Time", i, r.values[i])
			}
			*target = value
		default:
			return fmt.Errorf("scan: unsupported destination type %T", d)
		}
	}
	return nil
}

func TestNewPostgresUserRepository(t *testing.T) {
	require.NotNil(t, NewPostgresUserRepository(nil))
}

func TestPostgresUserRepository_Create(t *testing.T) {
	pool := &fakePool{}
	repo := &postgresUserRepository{pool: pool}

	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-password"), bcrypt.MinCost)
	require.NoError(t, err)

	workspaceID := uuid.NewString()
	user := &User{
		WorkspaceID:  workspaceID,
		Email:        "owner@example.com",
		PasswordHash: string(hash),
		Role:         "owner",
	}

	require.NoError(t, repo.Create(context.Background(), user))
	require.Contains(t, pool.execSQL, "INSERT INTO users")
	require.Len(t, pool.execArgs, 5)

	// The repository generates the user ID itself.
	id, ok := pool.execArgs[0].(string)
	require.True(t, ok)
	_, err = uuid.Parse(id)
	require.NoError(t, err, "generated user id must be a UUID")
	require.Equal(t, workspaceID, pool.execArgs[1])
	require.Equal(t, "owner@example.com", pool.execArgs[2])
	require.Equal(t, "owner", pool.execArgs[4])

	// The stored value must be a bcrypt hash of the original password.
	storedHash, ok := pool.execArgs[3].(string)
	require.True(t, ok)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("s3cret-password")))
	require.Error(t, bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("wrong-password")))
}

func TestPostgresUserRepository_CreateError(t *testing.T) {
	pool := &fakePool{execErr: errors.New("unique violation")}
	repo := &postgresUserRepository{pool: pool}

	err := repo.Create(context.Background(), &User{WorkspaceID: uuid.NewString(), Email: "dup@example.com", Role: "viewer"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "create user")
	require.Contains(t, err.Error(), "unique violation")
}

func TestPostgresUserRepository_GetByEmail(t *testing.T) {
	createdAt := time.Now().UTC().Truncate(time.Second)
	user := &User{
		ID:           uuid.NewString(),
		WorkspaceID:  uuid.NewString(),
		Email:        "admin@example.com",
		PasswordHash: "$2a$10$hash",
		Role:         "admin",
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	pool := &fakePool{row: fakeRow{values: []any{
		user.ID, user.WorkspaceID, user.Email, user.PasswordHash, user.Role, user.CreatedAt, user.UpdatedAt,
	}}}
	repo := &postgresUserRepository{pool: pool}

	got, err := repo.GetByEmail(context.Background(), "admin@example.com")
	require.NoError(t, err)
	require.Equal(t, user, got)
	require.Contains(t, pool.querySQL, "FROM users")
	require.Equal(t, []any{"admin@example.com"}, pool.queryArgs)
}

func TestPostgresUserRepository_GetByEmailNotFound(t *testing.T) {
	pool := &fakePool{row: fakeRow{err: pgx.ErrNoRows}}
	repo := &postgresUserRepository{pool: pool}

	got, err := repo.GetByEmail(context.Background(), "missing@example.com")
	require.Nil(t, got)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.Contains(t, err.Error(), "get user by email")
}

func TestPostgresUserRepository_GetByID(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pool := &fakePool{row: fakeRow{values: []any{
		"user-1", "workspace-1", "viewer@example.com", "$2a$10$hash", "viewer", now, now,
	}}}
	repo := &postgresUserRepository{pool: pool}

	got, err := repo.GetByID(context.Background(), "user-1")
	require.NoError(t, err)
	require.Equal(t, &User{
		ID:           "user-1",
		WorkspaceID:  "workspace-1",
		Email:        "viewer@example.com",
		PasswordHash: "$2a$10$hash",
		Role:         "viewer",
		CreatedAt:    now,
		UpdatedAt:    now,
	}, got)
	require.Contains(t, pool.querySQL, "WHERE id = $1")
	require.Equal(t, []any{"user-1"}, pool.queryArgs)
}

func TestPostgresUserRepository_GetByIDError(t *testing.T) {
	pool := &fakePool{row: fakeRow{err: errors.New("connection reset")}}
	repo := &postgresUserRepository{pool: pool}

	got, err := repo.GetByID(context.Background(), "user-1")
	require.Nil(t, got)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get user by id")
}
