package mcpserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/redis/go-redis/v9"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
	"github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/idempotency"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

// ── Counting executor ──────────────────────────────────────────

type countingExecutor struct {
	result map[string]any
	err    error
	calls  atomic.Int32
}

func (c *countingExecutor) Execute(_ context.Context, call Call) (map[string]any, error) {
	c.calls.Add(1)
	if c.err != nil {
		return nil, c.err
	}
	return c.result, nil
}

// ── Test helper ────────────────────────────────────────────────

func startTestRedis(t *testing.T) *idempotency.Cache {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := rediscontainer.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("failed to start Redis container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	redisOpts, err := redis.ParseURL(connStr)
	if err != nil {
		t.Fatalf("parse URL %q: %v", connStr, err)
	}
	client := redis.NewClient(redisOpts)
	t.Cleanup(func() { _ = client.Close() })

	if err := client.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return idempotency.New(client, nil, nil)
}

func newIdempotencyServer(t *testing.T, exec Executor, cache *idempotency.Cache) *Server {
	t.Helper()
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	broker := &fakeBroker{data: map[string]string{"token": "ghp_test"}}
	store := &audit.MemoryStore{}
	srv, err := NewServer(Options{
		Catalog:          cat,
		Authorizer:       engine,
		Auditor:          audit.NewLogger(store),
		Broker:           broker,
		Executor:         exec,
		IdempotencyCache: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func callToolWithMeta(t *testing.T, srv *Server, ctx context.Context, name string, args map[string]any, meta map[string]any) (*mcpgo.CallToolResult, error) {
	t.Helper()
	cl, err := mcpclient.NewInProcessClient(srv.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		t.Fatal(err)
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	if meta != nil {
		req.Params.Meta = &mcpgo.Meta{AdditionalFields: meta}
	}
	return cl.CallTool(ctx, req)
}

// ── Critical acceptance tests (issue #106) ─────────────────────

func TestMCP_ToolIdempotencyKey_RetryDoesNotExecuteTwice(t *testing.T) {
	cache := startTestRedis(t)
	counter := &countingExecutor{result: map[string]any{"status": "success", "pushed": true}}
	srv := newIdempotencyServer(t, counter, cache)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	idemKey := "idem-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": idemKey}

	// First call: miss → executor called.
	r1, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"}, meta)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if r1.IsError {
		t.Fatalf("first call error: %s", resultText(t, r1))
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls after first = %d, want 1", counter.calls.Load())
	}

	// Second call: same key → cache hit → executor NOT called.
	r2, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"}, meta)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r2.IsError {
		t.Fatalf("second call error: %s", resultText(t, r2))
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls after second = %d, want 1 (must not execute twice)", counter.calls.Load())
	}

	// Responses must be identical.
	txt1 := resultText(t, r1)
	txt2 := resultText(t, r2)
	if txt1 != txt2 {
		t.Errorf("responses differ:\n  first:  %s\n  second: %s", txt1, txt2)
	}
}

func TestMCP_ToolIdempotencyKey_NoKey_NoCache(t *testing.T) {
	cache := startTestRedis(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv := newIdempotencyServer(t, counter, cache)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))

	// Two calls without idempotency key → executor called twice.
	for i := 0; i < 2; i++ {
		_, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if counter.calls.Load() != 2 {
		t.Errorf("executor calls = %d, want 2 (no caching without key)", counter.calls.Load())
	}
}

func TestMCP_ToolIdempotencyKey_DifferentKey_DifferentCache(t *testing.T) {
	cache := startTestRedis(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv := newIdempotencyServer(t, counter, cache)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))

	if _, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, map[string]any{"idempotency_key": "key-A"}); err != nil {
		t.Fatalf("call key-A: %v", err)
	}
	if _, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, map[string]any{"idempotency_key": "key-B"}); err != nil {
		t.Fatalf("call key-B: %v", err)
	}

	if counter.calls.Load() != 2 {
		t.Errorf("executor calls = %d, want 2 (different keys)", counter.calls.Load())
	}
}

func TestMCP_ToolIdempotencyKey_WorkspaceIsolation(t *testing.T) {
	cache := startTestRedis(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv := newIdempotencyServer(t, counter, cache)

	idemKey := "shared-key"

	ctxA := identity.With(context.Background(), identity.Identity{
		WorkspaceID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		AuthorID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Role:          "developer",
		AutonomyLevel: "R1",
	})
	ctxB := identity.With(context.Background(), identity.Identity{
		WorkspaceID:   uuid.MustParse("AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"),
		AgentID:       uuid.MustParse("BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB"),
		AuthorID:      uuid.MustParse("CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC"),
		Role:          "developer",
		AutonomyLevel: "R1",
	})

	if _, err := callToolWithMeta(t, srv, ctxA, "git.push", map[string]any{"repo": "test"}, map[string]any{"idempotency_key": idemKey}); err != nil {
		t.Fatalf("workspace A call: %v", err)
	}
	if _, err := callToolWithMeta(t, srv, ctxB, "git.push", map[string]any{"repo": "test"}, map[string]any{"idempotency_key": idemKey}); err != nil {
		t.Fatalf("workspace B call: %v", err)
	}

	if counter.calls.Load() != 2 {
		t.Errorf("executor calls = %d, want 2 (workspace isolation)", counter.calls.Load())
	}
}

func TestMCP_ToolIdempotencyKey_ExecutorError_MarksFailed(t *testing.T) {
	cache := startTestRedis(t)
	counter := &countingExecutor{err: errors.New("upstream timeout")}
	srv := newIdempotencyServer(t, counter, cache)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	idemKey := "fail-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": idemKey}

	// First call: executor error.
	r1, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !r1.IsError {
		t.Fatal("expected error result")
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", counter.calls.Load())
	}

	// Retry after error: cache cleared, executor called again.
	counter.err = nil
	counter.result = map[string]any{"status": "success"}
	r2, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if r2.IsError {
		t.Fatalf("retry error: %s", resultText(t, r2))
	}
	if counter.calls.Load() != 2 {
		t.Errorf("executor calls after retry = %d, want 2 (MarkFailed cleared cache)", counter.calls.Load())
	}
}

func TestMCP_ToolIdempotencyKey_RedisDown_FailOpen(t *testing.T) {
	// Unreachable Redis → fail-open, executor always called.
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	cache := idempotency.New(client, nil, nil)

	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv := newIdempotencyServer(t, counter, cache)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	idemKey := "failopen-" + uuid.New().String()

	for i := 0; i < 2; i++ {
		r, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, map[string]any{"idempotency_key": idemKey})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if r.IsError {
			t.Fatalf("call %d error (fail-open should succeed): %s", i, resultText(t, r))
		}
	}
	if counter.calls.Load() != 2 {
		t.Errorf("executor calls = %d, want 2 (fail-open, no caching)", counter.calls.Load())
	}
	_ = client.Close()
}
