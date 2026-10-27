package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTaskRunPool struct {
	tx       *fakeTaskRunTx
	beginErr error
	pingErr  error
}

func (p fakeTaskRunPool) Begin(context.Context) (taskRunTx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

func (p fakeTaskRunPool) Ping(context.Context) error { return p.pingErr }

type fakeTaskRunTx struct {
	row        pgx.Row
	execErr    error
	commitErr  error
	execSQL    string
	querySQL   string
	execArgs   []any
	queryArgs  []any
	committed  bool
	rolledBack bool
}

func (tx *fakeTaskRunTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execSQL = sql
	tx.execArgs = args
	return pgconn.NewCommandTag("SELECT 1"), tx.execErr
}

func (tx *fakeTaskRunTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.querySQL = sql
	tx.queryArgs = args
	return tx.row
}

func (tx *fakeTaskRunTx) Commit(context.Context) error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeTaskRunTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type fakeTaskRunRow struct {
	path *string
	err  error
}

func (r fakeTaskRunRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	value := dest[0].(**string)
	*value = r.path
	return nil
}

func TestWorkspacePathScopesLookupToTenant(t *testing.T) {
	path := "/workspaces/task-1"
	tx := &fakeTaskRunTx{row: fakeTaskRunRow{path: &path}}
	store := &PostgresTaskRunStore{pool: fakeTaskRunPool{tx: tx}}
	taskRunID, workspaceID := uuid.New(), uuid.New()

	got, err := store.WorkspacePath(context.Background(), taskRunID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got != path || !tx.committed {
		t.Fatalf("path/commit = %q/%v", got, tx.committed)
	}
	if !strings.Contains(tx.execSQL, "set_config('app.current_workspace_id'") || tx.execArgs[0] != workspaceID.String() {
		t.Fatalf("tenant scope SQL/args = %q / %v", tx.execSQL, tx.execArgs)
	}
	if !strings.Contains(tx.querySQL, "workspace_id = $2") || tx.queryArgs[0] != taskRunID || tx.queryArgs[1] != workspaceID {
		t.Fatalf("tenant-scoped task query = %q / %v", tx.querySQL, tx.queryArgs)
	}
}

func TestWorkspacePathReturnsEmptyForNull(t *testing.T) {
	tx := &fakeTaskRunTx{row: fakeTaskRunRow{}}
	store := &PostgresTaskRunStore{pool: fakeTaskRunPool{tx: tx}}
	path, err := store.WorkspacePath(context.Background(), uuid.New(), uuid.New())
	if err != nil || path != "" {
		t.Fatalf("path/error = %q/%v", path, err)
	}
}

func TestWorkspacePathReturnsTenantNotFound(t *testing.T) {
	tx := &fakeTaskRunTx{row: fakeTaskRunRow{err: pgx.ErrNoRows}}
	store := &PostgresTaskRunStore{pool: fakeTaskRunPool{tx: tx}}
	_, err := store.WorkspacePath(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrTaskRunNotFound) || !tx.rolledBack {
		t.Fatalf("error/rollback = %v/%v", err, tx.rolledBack)
	}
}

func TestWorkspacePathPropagatesDatabaseErrors(t *testing.T) {
	tests := []struct {
		name string
		pool taskRunPool
	}{
		{name: "begin", pool: fakeTaskRunPool{beginErr: errors.New("begin failed")}},
		{name: "scope", pool: fakeTaskRunPool{tx: &fakeTaskRunTx{execErr: errors.New("scope failed")}}},
		{name: "query", pool: fakeTaskRunPool{tx: &fakeTaskRunTx{row: fakeTaskRunRow{err: errors.New("query failed")}}}},
		{name: "commit", pool: fakeTaskRunPool{tx: &fakeTaskRunTx{row: fakeTaskRunRow{}, commitErr: errors.New("commit failed")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &PostgresTaskRunStore{pool: test.pool}
			if _, err := store.WorkspacePath(context.Background(), uuid.New(), uuid.New()); err == nil {
				t.Fatal("expected database error")
			}
		})
	}
}

func TestTaskRunStoreReadiness(t *testing.T) {
	store := &PostgresTaskRunStore{pool: fakeTaskRunPool{pingErr: errors.New("database down")}}
	if err := store.Ready(context.Background()); err == nil {
		t.Fatal("expected readiness error")
	}
}

func TestNewPostgresTaskRunStore(t *testing.T) {
	if NewPostgresTaskRunStore(nil) == nil {
		t.Fatal("constructor returned a nil store")
	}
}
