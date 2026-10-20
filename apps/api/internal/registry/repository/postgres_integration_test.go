package repository

import (
	"context"
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
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresRegistryCRUDCloneRevertAndRLS(t *testing.T) {
	if output, err := exec.CommandContext(timeoutContext(t, 10*time.Second), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker is unavailable; skipping PostgreSQL integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := postgrescontainer.Run(ctx, "postgres:18-alpine",
		postgrescontainer.WithDatabase("registry_test"),
		postgrescontainer.WithUsername("registry_test"),
		postgrescontainer.WithPassword("registry_test"),
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
	applyRegistryMigration(t, ctx, pool)
	if _, err := pool.Exec(ctx, `CREATE ROLE registry_app NOLOGIN NOBYPASSRLS`); err != nil {
		t.Fatalf("create non-bypass Registry role: %v", err)
	}
	if _, err := pool.Exec(ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON roles, teams, processes TO registry_app`); err != nil {
		t.Fatalf("create non-bypass Registry role: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE registry_app`)
		return err
	}
	registryPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create Registry database pool: %v", err)
	}
	t.Cleanup(registryPool.Close)

	workspaceID, authorID, approverID := uuid.New(), uuid.New(), uuid.New()
	createWorkspace(t, ctx, pool, workspaceID, authorID)
	tenantCtx := identity.WithTenant(ctx, workspaceID, authorID)
	roles := NewPostgresRoleRepository(registryPool)
	teams := NewPostgresTeamRepository(registryPool)
	processes := NewPostgresProcessRepository(registryPool)

	role := domain.NewRole(workspaceID, authorID, "backend-developer", domain.CategoryEngineering, domain.RoleSpec{
		Description: "original",
		Tools:       domain.ToolsSpec{Allow: []string{"fs.read"}},
	})
	if err := roles.Create(tenantCtx, role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if got, err := roles.GetByNameAndVersion(tenantCtx, role.Name, role.VersionString()); err != nil || got.ID != role.ID {
		t.Fatalf("get role by name and version = %#v, %v", got, err)
	}
	if rows, err := roles.List(tenantCtx, string(role.Category), string(domain.StatusDraft)); err != nil || len(rows) != 1 {
		t.Fatalf("filtered role list = %d rows, %v", len(rows), err)
	}
	if rows, err := roles.List(tenantCtx, "", ""); err != nil || len(rows) != 1 {
		t.Fatalf("unfiltered role list = %d rows, %v", len(rows), err)
	}
	if err := roles.Update(tenantCtx, uuid.New(), role.Spec); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing role error = %v, want ErrNotFound", err)
	}
	if _, err := roles.GetByID(tenantCtx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing role error = %v, want ErrNotFound", err)
	}
	gotRole, err := roles.GetByID(tenantCtx, role.ID)
	if err != nil || gotRole.VersionString() != "1.0.0" {
		t.Fatalf("get created role = %#v, %v", gotRole, err)
	}
	role.Spec.Description = "updated"
	if err := roles.Update(tenantCtx, role.ID, role.Spec); err != nil {
		t.Fatalf("update draft role: %v", err)
	}
	if err := roles.UpdateStatus(tenantCtx, role.ID, domain.StatusApproved, authorID); !errors.Is(err, domain.ErrApproverIsAuthor) {
		t.Fatalf("self-approval error = %v, want ErrApproverIsAuthor", err)
	}
	if err := roles.UpdateStatus(tenantCtx, role.ID, domain.StatusApproved, approverID); err != nil {
		t.Fatalf("approve role: %v", err)
	}
	if err := roles.UpdateStatus(tenantCtx, role.ID, domain.StatusDraft, approverID); !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Fatalf("invalid role status transition error = %v, want ErrInvalidStatusTransition", err)
	}
	if err := roles.Update(tenantCtx, role.ID, role.Spec); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("update approved role error = %v, want ErrImmutable", err)
	}
	if _, err := roles.Revert(tenantCtx, role.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revert first role version error = %v, want ErrNotFound", err)
	}
	if _, err := roles.Clone(tenantCtx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("clone missing role error = %v, want ErrNotFound", err)
	}
	clone, err := roles.Clone(tenantCtx, role.ID)
	if err != nil || clone.VersionString() != "1.1.0" || clone.Status != domain.StatusDraft {
		t.Fatalf("clone role = %#v, %v", clone, err)
	}
	reverted, err := roles.Revert(tenantCtx, clone.ID)
	if err != nil || reverted.VersionString() != "1.2.0" || reverted.Spec.Description != "updated" {
		t.Fatalf("revert role = %#v, %v", reverted, err)
	}

	team := domain.NewTeam(workspaceID, authorID, "mvp-startup", domain.TeamSpec{
		Description:  "team",
		Topology:     domain.TopologyOrchestrated,
		Members:      []domain.TeamMemberSpec{{RoleName: "backend-developer", RoleVersion: "1.0.0", Count: 1}},
		Budget:       domain.BudgetSpec{MaxCostUSD: 5, MaxSteps: 20},
		ReviewMatrix: domain.ReviewMatrixSpec{RequiredReviewers: 1},
	})
	if err := teams.Create(tenantCtx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	if got, err := teams.GetByID(tenantCtx, team.ID); err != nil || got.ID != team.ID {
		t.Fatalf("get team by ID = %#v, %v", got, err)
	}
	if got, err := teams.GetByNameAndVersion(tenantCtx, team.Name, team.VersionString()); err != nil || got.ID != team.ID {
		t.Fatalf("get team by name and version = %#v, %v", got, err)
	}
	if rows, err := teams.List(tenantCtx, string(domain.StatusDraft)); err != nil || len(rows) != 1 {
		t.Fatalf("team list = %d rows, %v", len(rows), err)
	}
	if err := teams.Update(tenantCtx, team.ID, team.Spec); err != nil {
		t.Fatalf("update draft team: %v", err)
	}
	if _, err := teams.GetByID(tenantCtx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing team error = %v, want ErrNotFound", err)
	}
	if err := teams.UpdateStatus(tenantCtx, team.ID, domain.StatusApproved, authorID); !errors.Is(err, domain.ErrApproverIsAuthor) {
		t.Fatalf("team self-approval error = %v, want ErrApproverIsAuthor", err)
	}
	if err := teams.UpdateStatus(tenantCtx, team.ID, domain.StatusApproved, approverID); err != nil {
		t.Fatalf("approve team: %v", err)
	}
	if err := teams.UpdateStatus(tenantCtx, team.ID, domain.StatusDraft, approverID); !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Fatalf("invalid team status transition error = %v, want ErrInvalidStatusTransition", err)
	}
	if err := teams.Update(tenantCtx, team.ID, team.Spec); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("update approved team error = %v, want ErrImmutable", err)
	}
	if _, err := teams.Revert(tenantCtx, team.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revert first team version error = %v, want ErrNotFound", err)
	}
	teamClone, err := teams.Clone(tenantCtx, team.ID)
	if err != nil || teamClone.VersionString() != "1.1.0" {
		t.Fatalf("clone team = %#v, %v", teamClone, err)
	}
	teamRevert, err := teams.Revert(tenantCtx, teamClone.ID)
	if err != nil || teamRevert.VersionString() != "1.2.0" {
		t.Fatalf("revert team = %#v, %v", teamRevert, err)
	}

	process := domain.NewProcess(workspaceID, authorID, "default-sdlc", domain.ProcessSpec{
		Description: "process",
		Stages:      []domain.StageSpec{{Name: "Idea", Order: 0, Gate: "review"}},
	})
	if err := processes.Create(tenantCtx, process); err != nil {
		t.Fatalf("create process: %v", err)
	}
	if got, err := processes.GetByID(tenantCtx, process.ID); err != nil || got.ID != process.ID {
		t.Fatalf("get process by ID = %#v, %v", got, err)
	}
	if got, err := processes.GetByNameAndVersion(tenantCtx, process.Name, process.VersionString()); err != nil || got.ID != process.ID {
		t.Fatalf("get process by name and version = %#v, %v", got, err)
	}
	if rows, err := processes.List(tenantCtx, string(domain.StatusDraft)); err != nil || len(rows) != 1 {
		t.Fatalf("process list = %d rows, %v", len(rows), err)
	}
	if err := processes.Update(tenantCtx, process.ID, process.Spec); err != nil {
		t.Fatalf("update draft process: %v", err)
	}
	if _, err := processes.GetByID(tenantCtx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing process error = %v, want ErrNotFound", err)
	}
	if err := processes.UpdateStatus(tenantCtx, process.ID, domain.StatusApproved, authorID); !errors.Is(err, domain.ErrApproverIsAuthor) {
		t.Fatalf("process self-approval error = %v, want ErrApproverIsAuthor", err)
	}
	if err := processes.UpdateStatus(tenantCtx, process.ID, domain.StatusApproved, approverID); err != nil {
		t.Fatalf("approve process: %v", err)
	}
	if err := processes.UpdateStatus(tenantCtx, process.ID, domain.StatusDraft, approverID); !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Fatalf("invalid process status transition error = %v, want ErrInvalidStatusTransition", err)
	}
	if err := processes.Update(tenantCtx, process.ID, process.Spec); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("update approved process error = %v, want ErrImmutable", err)
	}
	if _, err := processes.Revert(tenantCtx, process.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revert first process version error = %v, want ErrNotFound", err)
	}
	processClone, err := processes.Clone(tenantCtx, process.ID)
	if err != nil || processClone.VersionString() != "1.1.0" {
		t.Fatalf("clone process = %#v, %v", processClone, err)
	}
	processRevert, err := processes.Revert(tenantCtx, processClone.ID)
	if err != nil || processRevert.VersionString() != "1.2.0" {
		t.Fatalf("revert process = %#v, %v", processRevert, err)
	}

	otherTenant := identity.WithTenant(ctx, uuid.New(), authorID)
	if _, err := roles.GetByID(ctx, role.ID); !errors.Is(err, identity.ErrMissingTenantContext) {
		t.Fatalf("role lookup without tenant context = %v, want ErrMissingTenantContext", err)
	}
	if _, err := roles.GetByID(otherTenant, role.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace role lookup error = %v, want ErrNotFound", err)
	}
	if rows, err := roles.List(otherTenant, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("cross-workspace role list = %d rows, %v", len(rows), err)
	}
	if _, err := teams.GetByID(otherTenant, team.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace team lookup error = %v, want ErrNotFound", err)
	}
	if rows, err := teams.List(otherTenant, ""); err != nil || len(rows) != 0 {
		t.Fatalf("cross-workspace team list = %d rows, %v", len(rows), err)
	}
	if _, err := processes.GetByID(otherTenant, process.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace process lookup error = %v, want ErrNotFound", err)
	}
	if rows, err := processes.List(otherTenant, ""); err != nil || len(rows) != 0 {
		t.Fatalf("cross-workspace process list = %d rows, %v", len(rows), err)
	}
	tx, err := registryPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM roles`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("RLS exposed %d roles outside the active workspace", visible)
	}
}

func timeoutContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func applyRegistryMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	root := findRepositoryRoot(t)
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "postgres", "0008_init_registry.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE workspaces (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create workspace fixture: %v", err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
		t.Fatalf("apply Registry migration: %v", err)
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

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0008_init_registry.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
