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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRepositoryOperations(t *testing.T) {
	pool := newRepositoryTestPool(t)
	repo := New(pool)
	if repo.Pool() != pool {
		t.Fatal("Pool() did not return the configured pool")
	}

	workspaceID, authorID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, authorID)
	if err := repo.RecordSuccess(ctx, ModelKey{Provider: "openai", ModelID: "gpt-test"}); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}

	call := &domain.LogicalCall{ProfileName: "code-strong"}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.CreateLogicalCall(canceledCtx, call); err != nil {
		t.Fatalf("CreateLogicalCall with canceled caller context: %v", err)
	}
	if call.ID == uuid.Nil || call.WorkspaceID != workspaceID || call.StartedAt.IsZero() {
		t.Fatalf("CreateLogicalCall did not populate persisted fields: %+v", call)
	}

	loaded, err := repo.GetLogicalCall(ctx, call.ID)
	if err != nil {
		t.Fatalf("GetLogicalCall: %v", err)
	}
	if loaded.ID != call.ID || loaded.WorkspaceID != workspaceID || loaded.ProfileName != call.ProfileName {
		t.Errorf("GetLogicalCall() = %+v, want the created call", loaded)
	}
	if _, err := repo.GetLogicalCall(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetLogicalCall(missing) error = %v, want ErrNotFound", err)
	}

	now := time.Now().UTC()
	call.ModelIDUsed = "gpt-test"
	call.ProviderUsed = "openai"
	call.Status = "succeeded"
	call.AttemptCount = 2
	call.CompletedAt = &now
	if err := repo.FinishLogicalCall(ctx, call); err != nil {
		t.Fatalf("FinishLogicalCall: %v", err)
	}
	if err := repo.FinishLogicalCall(ctx, &domain.LogicalCall{ID: uuid.New()}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("FinishLogicalCall(missing) error = %v, want ErrNotFound", err)
	}

	for _, attemptNum := range []int{2, 1} {
		attempt := &domain.Attempt{
			LogicalCallID: call.ID,
			AttemptNum:    attemptNum,
			Provider:      "openai",
			ModelID:       "gpt-test",
			Status:        "succeeded",
			CompletedAt:   &now,
		}
		if err := repo.InsertAttempt(ctx, attempt); err != nil {
			t.Fatalf("InsertAttempt(%d): %v", attemptNum, err)
		}
		if attempt.ID == uuid.Nil || attempt.StartedAt.IsZero() {
			t.Errorf("InsertAttempt(%d) did not populate persisted fields: %+v", attemptNum, attempt)
		}
	}
	if err := repo.InsertAttempt(ctx, &domain.Attempt{LogicalCallID: call.ID, Status: "in_progress"}); err == nil {
		t.Error("InsertAttempt accepted a status rejected by the database constraint")
	}

	attempts, err := repo.ListAttempts(ctx, call.ID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0].AttemptNum != 1 || attempts[1].AttemptNum != 2 {
		t.Errorf("ListAttempts() = %+v, want attempts ordered [1, 2]", attempts)
	}
	if attempts, err = repo.ListAttempts(ctx, uuid.New()); err != nil || len(attempts) != 0 {
		t.Errorf("ListAttempts(empty) = %v, %v; want empty, nil", attempts, err)
	}

	key := ModelKey{Provider: "openai", ModelID: "gpt-test"}
	cooldown := time.Now().UTC().Add(time.Minute)
	if err := repo.RecordAvailabilityFailure(ctx, key, "timeout", cooldown); err != nil {
		t.Fatalf("RecordAvailabilityFailure: %v", err)
	}
	if err := repo.RecordConfigError(ctx, key, "unauthorized"); err != nil {
		t.Fatalf("RecordConfigError: %v", err)
	}
	health, err := repo.HealthFor(ctx, []ModelKey{key})
	if err != nil {
		t.Fatalf("HealthFor after config error: %v", err)
	}
	if got := health[key]; got == nil || got.ConsecutiveFailures != 1 ||
		got.CooldownUntil == nil || !got.CooldownUntil.Equal(cooldown) ||
		got.LastErrorType == nil || *got.LastErrorType != "unauthorized" {
		t.Errorf("config error changed availability state: %+v", got)
	}
	if err := repo.RecordSuccess(ctx, key); err != nil {
		t.Fatalf("RecordSuccess(update): %v", err)
	}
	// These rows match the SQL arrays' cross-product but not either requested pair.
	for _, extra := range []ModelKey{{Provider: "openai", ModelID: "other"}, {Provider: "anthropic", ModelID: "gpt-test"}} {
		if err := repo.RecordSuccess(ctx, extra); err != nil {
			t.Fatalf("RecordSuccess(%+v): %v", extra, err)
		}
	}

	if health, err := repo.HealthFor(ctx, nil); err != nil || len(health) != 0 {
		t.Errorf("HealthFor(empty) = %v, %v; want empty, nil", health, err)
	}
	health, err = repo.HealthFor(ctx, []ModelKey{key})
	if err != nil {
		t.Fatalf("HealthFor: %v", err)
	}
	if got := health[key]; got == nil || got.ConsecutiveFailures != 0 ||
		got.CooldownUntil != nil || got.LastSuccessAt == nil {
		t.Errorf("HealthFor(%+v) = %+v, want successful reset", key, got)
	}
	pairs, err := repo.HealthFor(ctx, []ModelKey{key, {Provider: "anthropic", ModelID: "other"}})
	if err != nil {
		t.Fatalf("HealthFor(cross-product): %v", err)
	}
	if len(pairs) != 1 {
		t.Errorf("HealthFor(cross-product) returned %d rows, want only requested pairs", len(pairs))
	}

	if err := repo.CreateLogicalCall(context.Background(), &domain.LogicalCall{ProfileName: "missing-tenant"}); !errors.Is(err, identity.ErrMissingTenantContext) {
		t.Errorf("CreateLogicalCall without tenant error = %v, want ErrMissingTenantContext", err)
	}
}

func newRepositoryTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(checkCtx, "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for repository integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping repository integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("repository_test"),
		postgrescontainer.WithUsername("repository_test"),
		postgrescontainer.WithPassword("repository_test"),
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
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}

	migrationsDir := repositoryMigrationsDir(t)
	migration, err := os.ReadFile(filepath.Join(migrationsDir, "0010_llm_gateway.up.sql"))
	if err != nil {
		t.Fatalf("read gateway migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply gateway migration: %v", err)
	}
	return pool
}

func repositoryMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "migrations", "postgres", "0010_llm_gateway.up.sql")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Join(dir, "migrations", "postgres")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate gateway migration")
		}
		dir = parent
	}
}
