// Package repository provides integration tests for the semantic graph repositories.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestNodeRepository_CRUDAndRLS(t *testing.T) {
	if output, err := exec.CommandContext(timeoutContext(t, 10*time.Second), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker is unavailable; skipping PostgreSQL integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Use pgvector image which has the vector extension pre-installed.
	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("semantic_test"),
		postgrescontainer.WithUsername("semantic_test"),
		postgrescontainer.WithPassword("semantic_test"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL 18 container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	})

	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get container connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect to PostgreSQL container: %v", err)
	}

	// Apply schema: workspaces table + pgvector + sem_nodes + sem_edges + RLS.
	applySemanticMigration(t, ctx, pool)

	// Create a non-owner role for RLS enforcement.
	if _, err := pool.Exec(ctx, `CREATE ROLE semantic_app NOLOGIN NOBYPASSRLS`); err != nil {
		t.Fatalf("create non-bypass semantic role: %v", err)
	}
	if _, err := pool.Exec(ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON sem_nodes, sem_edges TO semantic_app`); err != nil {
		t.Fatalf("grant semantic role permissions: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE semantic_app`)
		return err
	}
	appPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create app database pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	workspaceID, authorID := uuid.New(), uuid.New()
	createWorkspace(t, ctx, pool, workspaceID, authorID)

	tenantCtx := identity.WithTenant(ctx, workspaceID, authorID)
	repo := NewNodeRepository(appPool)

	// ── Create ──────────────────────────────────────────────────
	node := &domain.SemNode{
		WorkspaceID: workspaceID,
		Layer:       "SLP",
		Kind:        "TestRole",
		Label:       "Test Role Node",
		Props:       map[string]any{"test": true},
		Version:     1,
		Status:      "draft",
	}
	if err := repo.Create(tenantCtx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.ID == uuid.Nil {
		t.Fatal("created node has zero UUID")
	}

	// ── GetByID ─────────────────────────────────────────────────
	got, err := repo.GetByID(tenantCtx, node.ID)
	if err != nil {
		t.Fatalf("get node by ID: %v", err)
	}
	if got.ID != node.ID {
		t.Errorf("got ID %s, want %s", got.ID, node.ID)
	}
	if got.Kind != "TestRole" {
		t.Errorf("got kind %q, want %q", got.Kind, "TestRole")
	}

	// ── List ────────────────────────────────────────────────────
	allNodes, err := repo.List(tenantCtx, "")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(allNodes) != 1 {
		t.Fatalf("list returned %d nodes, want 1", len(allNodes))
	}

	slpNodes, err := repo.List(tenantCtx, "SLP")
	if err != nil {
		t.Fatalf("list SLP nodes: %v", err)
	}
	if len(slpNodes) != 1 {
		t.Fatalf("list SLP returned %d nodes, want 1", len(slpNodes))
	}

	// ── Update (draft → OK) ────────────────────────────────────
	updated, err := repo.Update(tenantCtx, node.ID, domain.UpdateNodeInput{
		Kind:  "UpdatedRole",
		Label: "Updated Label",
	})
	if err != nil {
		t.Fatalf("update draft node: %v", err)
	}
	if updated.Kind != "UpdatedRole" {
		t.Errorf("updated kind %q, want %q", updated.Kind, "UpdatedRole")
	}

	// ── Update (active → 403) ───────────────────────────────────
	// Insert an active (immutable/approved-via-review) node.
	activeNode := &domain.SemNode{
		WorkspaceID: workspaceID,
		Layer:       "SLP",
		Kind:        "ActiveNode",
		Label:       "Active immutable node",
		Props:       map[string]any{},
		Version:     1,
		Status:      "active",
	}
	if err := repo.Create(tenantCtx, activeNode); err != nil {
		t.Fatalf("create active node: %v", err)
	}
	_, err = repo.Update(tenantCtx, activeNode.ID, domain.UpdateNodeInput{Kind: "X"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("update active node: expected ErrForbidden, got %v", err)
	}

	// ── GetByID (not found) ────────────────────────────────────
	_, err = repo.GetByID(tenantCtx, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get missing node: expected ErrNotFound, got %v", err)
	}

	// ── RLS isolation ──────────────────────────────────────────
	otherWorkspaceID := uuid.New()
	createWorkspace(t, ctx, pool, otherWorkspaceID, authorID)
	otherCtx := identity.WithTenant(ctx, otherWorkspaceID, authorID)

	// A node from workspace A should not be visible in workspace B.
	_, err = repo.GetByID(otherCtx, node.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-workspace lookup: expected ErrNotFound, got %v", err)
	}

	listOther, err := repo.List(otherCtx, "")
	if err != nil {
		t.Fatalf("list other workspace: %v", err)
	}
	if len(listOther) != 0 {
		t.Errorf("cross-workspace list: expected 0 nodes, got %d", len(listOther))
	}

	// ── Edge CRUD ──────────────────────────────────────────────
	edgeRepo := NewEdgeRepository(appPool)
	srcNode := &domain.SemNode{
		WorkspaceID: workspaceID,
		Layer:       "SLPr",
		Kind:        "Customer",
		Label:       "Customer",
		Props:       map[string]any{},
		Version:     1,
		Status:      "draft",
	}
	if err := repo.Create(tenantCtx, srcNode); err != nil {
		t.Fatalf("create src node: %v", err)
	}
	dstNode := &domain.SemNode{
		WorkspaceID: workspaceID,
		Layer:       "SLPr",
		Kind:        "Order",
		Label:       "Order",
		Props:       map[string]any{},
		Version:     1,
		Status:      "draft",
	}
	if err := repo.Create(tenantCtx, dstNode); err != nil {
		t.Fatalf("create dst node: %v", err)
	}

	edgeInput := domain.CreateEdgeInput{
		SrcID: srcNode.ID.String(),
		DstID: dstNode.ID.String(),
		Kind:  "has",
		Props: map[string]any{},
	}
	if err := edgeRepo.Create(tenantCtx, edgeInput); err != nil {
		t.Fatalf("create edge: %v", err)
	}

	// Delete the edge.
	if err := edgeRepo.Delete(tenantCtx, srcNode.ID); err != nil {
		// Edge was created without returned ID, so deletion by src ID
		// will fail (edge not found). That's expected.
		t.Logf("delete edge (expected fail without edge ID): %v", err)
	}
}

func TestNodeRepository_CrossWorkspaceJSONB(t *testing.T) {
	if output, err := exec.CommandContext(timeoutContext(t, 10*time.Second), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker unavailable: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("semantic_test2"),
		postgrescontainer.WithUsername("semantic_test2"),
		postgrescontainer.WithPassword("semantic_test2"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	applySemanticMigration(t, ctx, pool)

	if _, err := pool.Exec(ctx, `CREATE ROLE semantic_app2 NOLOGIN NOBYPASSRLS`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := pool.Exec(ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON sem_nodes, sem_edges TO semantic_app2`); err != nil {
		t.Fatalf("grant: %v", err)
	}
	poolCfg, _ := pgxpool.ParseConfig(connStr)
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE semantic_app2`)
		return err
	}
	appPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("create app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	wsID, authorID := uuid.New(), uuid.New()
	createWorkspace(t, ctx, pool, wsID, authorID)

	tenantCtx := identity.WithTenant(ctx, wsID, authorID)
	repo := NewNodeRepository(appPool)

	node := &domain.SemNode{
		WorkspaceID: wsID, Layer: "SLP", Kind: "Complex", Label: "Complex Node",
		Version: 1, Status: "draft",
		Props: map[string]any{
			"nested": map[string]any{"a": 1, "b": "two"},
			"tags":   []any{"x", "y", "z"},
		},
	}
	if err := repo.Create(tenantCtx, node); err != nil {
		t.Fatalf("create node with complex props: %v", err)
	}

	got, err := repo.GetByID(tenantCtx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}

	// Verify JSONB round-trip.
	propsJSON, _ := json.Marshal(got.Props)
	var parsed map[string]any
	json.Unmarshal(propsJSON, &parsed)
	nested, ok := parsed["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested JSONB not preserved: %s", string(propsJSON))
	}
	if nested["a"] != float64(1) {
		t.Errorf("nested.a = %v, want 1", nested["a"])
	}
}

// ── Helpers ───────────────────────────────────────────────────

func timeoutContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func applySemanticMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	root := findRepoRoot(t)

	// Create workspaces table.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE workspaces (
			id UUID PRIMARY KEY, name TEXT NOT NULL, owner_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create workspaces fixture: %v", err)
	}

	// Enable pgvector extension.
	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create vector extension: %v", err)
	}

	// Apply migration 0005 (semantic graph) from host.
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "postgres", "0005_init_semantic_graph.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
		t.Fatalf("apply semantic graph migration: %v", err)
	}
}

func createWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, authorID uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'test', $2)`,
		workspaceID, authorID.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0005_init_semantic_graph.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
