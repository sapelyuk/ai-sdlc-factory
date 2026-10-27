package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrTaskRunNotFound = errors.New("task_run not found in workspace")

// TaskRunStore resolves a workspace path without allowing cross-tenant access.
type TaskRunStore interface {
	WorkspacePath(context.Context, uuid.UUID, uuid.UUID) (string, error)
	Ready(context.Context) error
}

// PostgresTaskRunStore reads the workspace directory from task_runs.
type PostgresTaskRunStore struct {
	pool taskRunPool
}

type taskRunPool interface {
	Begin(context.Context) (taskRunTx, error)
	Ping(context.Context) error
}

type taskRunTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type pgxTaskRunPool struct {
	pool *pgxpool.Pool
}

type pgxTaskRunTransaction struct {
	tx pgx.Tx
}

// NewPostgresTaskRunStore creates a PostgreSQL-backed task-run store.
func NewPostgresTaskRunStore(pool *pgxpool.Pool) *PostgresTaskRunStore {
	return &PostgresTaskRunStore{pool: pgxTaskRunPool{pool: pool}}
}

func (p pgxTaskRunPool) Begin(ctx context.Context) (taskRunTx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTaskRunTransaction{tx: tx}, nil
}

func (p pgxTaskRunPool) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (tx pgxTaskRunTransaction) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.tx.Exec(ctx, sql, args...)
}

func (tx pgxTaskRunTransaction) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.tx.QueryRow(ctx, sql, args...)
}

func (tx pgxTaskRunTransaction) Commit(ctx context.Context) error {
	return tx.tx.Commit(ctx)
}

func (tx pgxTaskRunTransaction) Rollback(ctx context.Context) error {
	return tx.tx.Rollback(ctx)
}

// WorkspacePath reads the path only when the task run belongs to workspaceID.
func (s *PostgresTaskRunStore) WorkspacePath(ctx context.Context, taskRunID, workspaceID uuid.UUID) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin task_run lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return "", fmt.Errorf("set task_run tenant scope: %w", err)
	}
	var workspacePath *string
	err = tx.QueryRow(ctx, `
		SELECT workspace_path
		FROM task_runs
		WHERE id = $1 AND workspace_id = $2
	`, taskRunID, workspaceID).Scan(&workspacePath)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTaskRunNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query task_run workspace_path: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit task_run lookup: %w", err)
	}
	if workspacePath == nil {
		return "", nil
	}
	return *workspacePath, nil
}

// Ready reports whether PostgreSQL is reachable.
func (s *PostgresTaskRunStore) Ready(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
