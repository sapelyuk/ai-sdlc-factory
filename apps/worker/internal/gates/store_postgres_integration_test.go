package gates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPostgresStoreCRUD exercises the PostgresStore with a real PostgreSQL
// container (issue #20). Skipped when Docker is unavailable.
func TestPostgresStoreCRUD(t *testing.T) {
	pool := startGatesPostgres(t, "gates_test", []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
		"0017_approvals_run_id.up.sql",
		"0018_approvals_run_id_set_null.up.sql",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	store := NewPostgresStore(pool)
	ws := uuid.New()
	author := uuid.New()
	approver := uuid.New()

	// Create workspace (needed for FK + RLS).
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'gates', $2)`,
		ws, author.String())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	// Create approval request.
	req := BuildApprovalRequest(ws, GateProdApproved, author, 2*time.Hour, map[string]any{
		"from": "deploy", "to": "release",
	})
	req.RiskLevel = "R3"
	req.WorkflowID = "wf-gates-1"

	require.NoError(t, store.Create(ctx, req))

	// Get.
	got, err := store.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, GateProdApproved, got.GateName)
	require.Equal(t, ws, got.WorkspaceID)
	require.Equal(t, "R3", got.RiskLevel)
	require.Equal(t, "wf-gates-1", got.WorkflowID)
	require.NotNil(t, got.DeadlineAt)

	// List pending.
	pending := ApprovalPending
	list, err := store.List(ctx, ws, &pending)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, req.ID, list[0].ID)

	// Approve.
	require.NoError(t, store.Decide(ctx, req.ID, true, approver, "looks good"))
	got, _ = store.Get(ctx, req.ID)
	require.Equal(t, ApprovalApproved, got.Status)
	require.NotNil(t, got.DecidedAt)

	// List approved.
	approved := ApprovalApproved
	list, err = store.List(ctx, ws, &approved)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Create a second request, reject it.
	req2 := BuildApprovalRequest(ws, GateRequirementsApproved, author, time.Hour, nil)
	require.NoError(t, store.Create(ctx, req2))
	require.NoError(t, store.Decide(ctx, req2.ID, false, approver, "not yet"))
	got2, _ := store.Get(ctx, req2.ID)
	require.Equal(t, ApprovalRejected, got2.Status)

	// List pending should show only req2's predecessor was approved, req2 is rejected...
	// Actually req2 was created as pending then immediately rejected, so it's not in pending.
	list, err = store.List(ctx, ws, &pending)
	require.NoError(t, err)
	require.Len(t, list, 0)

	// Expire a new request.
	req3 := BuildApprovalRequest(ws, GateArchitectureApproved, author, 0, nil)
	require.NoError(t, store.Create(ctx, req3))
	require.NoError(t, store.Expire(ctx, req3.ID))
	got3, _ := store.Get(ctx, req3.ID)
	require.Equal(t, ApprovalExpired, got3.Status)

	// Idempotent expire.
	require.NoError(t, store.Expire(ctx, req3.ID)) // no-op
	got3, _ = store.Get(ctx, req3.ID)
	require.Equal(t, ApprovalExpired, got3.Status)

	// Idempotent decide.
	require.Error(t, store.Decide(ctx, req.ID, false, approver, "too late"))  // already approved
	require.Error(t, store.Decide(ctx, req3.ID, false, approver, "too late")) // already expired
}

func TestPostgresStoreWorkspaceIsolation(t *testing.T) {
	pool := startGatesPostgres(t, "gates_iso", []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
		"0017_approvals_run_id.up.sql",
		"0018_approvals_run_id_set_null.up.sql",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	store := NewPostgresStore(pool)
	ws1, ws2 := uuid.New(), uuid.New()
	author := uuid.New()

	// Create both workspaces.
	for _, ws := range []uuid.UUID{ws1, ws2} {
		tx, _ := pool.Begin(ctx)
		_, _ = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
		_, _ = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'iso', $2)`,
			ws, author.String())
		_ = tx.Commit(ctx)
	}

	// Create a request in ws1.
	req := BuildApprovalRequest(ws1, GateProdApproved, author, time.Hour, nil)
	require.NoError(t, store.Create(ctx, req))

	// Store.Get reads via workspace_of which bypasses RLS (it uses the pool directly).
	// Then it runs inTenant with the resolved workspace. So it should work.
	got, err := store.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, ws1, got.WorkspaceID)

	// List for ws1 finds it.
	list, err := store.List(ctx, ws1, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// List for ws2 finds nothing (RLS).
	list, err = store.List(ctx, ws2, nil)
	require.NoError(t, err)
	require.Len(t, list, 0)
}

func TestPostgresStoreNilPool(t *testing.T) {
	s := NewPostgresStore(nil)
	err := s.Create(context.Background(), ApprovalRequest{})
	require.Error(t, err)

	_, err = s.Get(context.Background(), uuid.New())
	require.Error(t, err)

	_, err = s.List(context.Background(), uuid.New(), nil)
	require.Error(t, err)

	require.Error(t, s.Decide(context.Background(), uuid.New(), true, uuid.New(), ""))
	require.Error(t, s.Expire(context.Background(), uuid.New()))
}

// TestApprovalSurvivesRunDeletion verifies that deleting a run does not cascade-delete
// its approval. The approval must remain with run_id = NULL.
//
// Rationale: approval rows feed the audit trail (FR-OPS-1, hash chain in
// packages/sdk-go/audit). Migration 0017 created approvals.run_id with
// ON DELETE CASCADE, which would silently destroy that trail during run
// cleanup. Migration 0018 replaces the FK with ON DELETE SET NULL.
func TestApprovalSurvivesRunDeletion(t *testing.T) {
	pool := startGatesPostgres(t, "gates_fk", []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
		"0017_approvals_run_id.up.sql",
		"0018_approvals_run_id_set_null.up.sql",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	store := NewPostgresStore(pool)
	ws := uuid.New()
	author := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'del-run', $2)`,
		ws, author.String())
	require.NoError(t, err)

	// A run is the delete target; the approval points at it.
	runID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO runs (id, workspace_id, name) VALUES ($1, $2, 'run-to-delete')`,
		runID, ws)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	req := BuildApprovalRequest(ws, GateProdApproved, author, time.Hour, map[string]any{
		"from": "deploy", "to": "release",
	})
	req.RunID = &runID
	require.NoError(t, store.Create(ctx, req))

	// The link exists before the delete.
	got, err := store.Get(ctx, req.ID)
	require.NoError(t, err)
	require.NotNil(t, got.RunID, "approval should reference the run before deletion")
	require.Equal(t, runID, *got.RunID)

	// Deleting the run must not remove the approval.
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
	require.NoError(t, err)
	tag, err := tx.Exec(ctx, `DELETE FROM runs WHERE id = $1`, runID)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "the run row should be deleted")
	require.NoError(t, tx.Commit(ctx))

	// The approval survives with run_id nulled out.
	got, err = store.Get(ctx, req.ID)
	require.NoError(t, err, "approval must survive deletion of the referenced run")
	require.Nil(t, got.RunID, "run_id should be set to NULL, not cascade-deleted")
	require.Equal(t, ApprovalPending, got.Status)

	// The FK really carries SET NULL, not CASCADE. Read the rule back from the
	// catalog so a silent migration no-op cannot pass this test.
	//
	// Scope matters: approvals already carries cascading FKs from 0002
	// (task_run_id, stage_run_id, workspace_id), so a repo-wide "no cascade"
	// assertion would be wrong. Only the reference to runs(id) is in question.
	//
	// Checking a single expected constraint name is not enough either: if 0017
	// had auto-named the constraint differently, 0018's
	// `DROP CONSTRAINT IF EXISTS` would be a silent no-op and `ADD CONSTRAINT`
	// would leave TWO foreign keys on run_id, one still CASCADE. So assert that
	// every FK pointing at runs has exactly one row and that it is SET NULL.
	// In pg_constraint confdeltype is 'n' SET NULL, 'c' CASCADE, 'a' NO ACTION.
	type fkInfo struct {
		name string
		rule string
	}
	rows, err := pool.Query(ctx, `
		SELECT c.conname, c.confdeltype
		FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.confrelid
		WHERE c.conrelid = 'approvals'::regclass
		  AND c.contype = 'f'
		  AND r.relname = 'runs'
		ORDER BY c.conname`,
	)
	require.NoError(t, err)
	var fks []fkInfo
	for rows.Next() {
		var info fkInfo
		require.NoError(t, rows.Scan(&info.name, &info.rule))
		fks = append(fks, info)
	}
	rows.Close()

	require.Len(t, fks, 1, "approvals.run_id must carry exactly one FK to runs")
	require.Equal(t, "approvals_run_id_fkey", fks[0].name,
		"0018 must reuse the constraint name generated by 0017")
	require.Equal(t, "n", fks[0].rule, "approvals_run_id_fkey must be ON DELETE SET NULL")
}

// startGatesPostgres starts a PostgreSQL container, applies the named migrations
// and returns the connection pool.
//
// Docker availability is the only permitted skip reason, and never on CI: a
// container that cannot start in CI fails the build instead of silently dropping
// integration coverage to zero.
func startGatesPostgres(t *testing.T, dbName string, migrations []string) *pgxpool.Pool {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase(dbName),
		postgrescontainer.WithUsername(dbName),
		postgrescontainer.WithPassword(dbName),
		postgrescontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	for _, name := range migrations {
		contents, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "postgres", name))
		require.NoError(t, err, "read migration %s", name)
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
	return pool
}

// repoRoot walks up the directory tree to find the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0001_init_squads_and_agents.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
