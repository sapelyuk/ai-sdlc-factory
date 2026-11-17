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
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
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

type sequenceAuthorizer struct {
	decisions []policy.Decision
	calls     int
}

func (a *sequenceAuthorizer) Authorize(_ context.Context, req policy.Request) (policy.Decision, error) {
	a.calls++
	if len(a.decisions) == 0 {
		return policy.Decision{Allow: true, RiskLevel: req.ToolRiskLevel, Reason: "allowed"}, nil
	}
	decision := a.decisions[0]
	if len(a.decisions) > 1 {
		a.decisions = a.decisions[1:]
	} else {
		a.decisions = nil
	}
	if decision.RiskLevel == "" {
		decision.RiskLevel = req.ToolRiskLevel
	}
	return decision, nil
}

// ── Test helper ────────────────────────────────────────────────

// redisImage pins the same digest as deploy/compose/docker-compose.yml so the
// integration tests run against the exact image the stack ships (ADR-026).
const redisImage = "docker.io/library/redis:7-alpine@sha256:7f7d30b7fdd09467bac58198a1f166dd69789d348d4fe859cccb20e0cf287fbe"

func startTestRedisClient(t *testing.T) (*redis.Client, *idempotency.Cache) {
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

	container, err := rediscontainer.Run(ctx, redisImage)
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
	return client, idempotency.New(client, nil, nil)
}

func startTestRedis(t *testing.T) *idempotency.Cache {
	t.Helper()
	_, cache := startTestRedisClient(t)
	return cache
}

func newIdempotencyServer(t *testing.T, exec Executor, cache *idempotency.Cache) *Server {
	srv, _ := newIdempotencyServerWithAuthorizer(t, exec, cache, nil)
	return srv
}

func newIdempotencyServerWithAuthorizer(t *testing.T, exec Executor, cache *idempotency.Cache, auth policy.Authorizer) (*Server, *audit.MemoryStore) {
	t.Helper()
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	if auth == nil {
		engine, err := policy.NewEmbedded(policyDir)
		if err != nil {
			t.Fatal(err)
		}
		auth = engine
	}
	broker := &fakeBroker{data: map[string]string{"token": "ghp_test"}}
	store := &audit.MemoryStore{}
	srv, err := NewServer(Options{
		Catalog:          cat,
		Authorizer:       auth,
		Auditor:          audit.NewLogger(store),
		Broker:           broker,
		Executor:         exec,
		IdempotencyCache: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, store
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

func TestMCP_CacheHit_StillAuthorizesRevokedAgent(t *testing.T) {
	client, cache := startTestRedisClient(t)
	counter := &countingExecutor{result: map[string]any{"status": "success", "pushed": true}}
	auth := &sequenceAuthorizer{decisions: []policy.Decision{{Allow: true, RiskLevel: "R1", Reason: "allowed"}, {Allow: false, RiskLevel: "R1", Reason: "revoked"}}}
	srv, _ := newIdempotencyServerWithAuthorizer(t, counter, cache, auth)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	key := "revoke-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": key}

	if _, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"}, meta); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls after first = %d, want 1", counter.calls.Load())
	}

	res, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"}, meta)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("second call unexpectedly succeeded: %s", resultText(t, res))
	}
	if got := resultText(t, res); !strings.Contains(got, "policy denied") {
		t.Fatalf("second call error = %q, want policy denied", got)
	}
	if auth.calls != 2 {
		t.Fatalf("Authorize calls = %d, want 2 (re-check on cache hit)", auth.calls)
	}
	if err := client.Get(context.Background(), "idem:tool:11111111-1111-1111-1111-111111111111:"+key).Err(); err == nil {
		t.Fatal("cache entry still exists after denied recheck")
	} else if !errors.Is(err, redis.Nil) {
		t.Fatalf("cache lookup error = %v, want redis.Nil", err)
	}
}

func TestMCP_DeniedRequest_DoesNotClaimCache(t *testing.T) {
	client, cache := startTestRedisClient(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	auth := &sequenceAuthorizer{decisions: []policy.Decision{{Allow: false, RiskLevel: "R1", Reason: "denied"}}}
	srv, _ := newIdempotencyServerWithAuthorizer(t, counter, cache, auth)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	key := "denied-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": key}
	res, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"}, meta)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "policy denied") {
		t.Fatalf("call error = %v, want policy denied", res)
	}
	if counter.calls.Load() != 0 {
		t.Fatalf("executor calls = %d, want 0", counter.calls.Load())
	}
	if err := client.Get(context.Background(), "idem:tool:11111111-1111-1111-1111-111111111111:"+key).Err(); err == nil {
		t.Fatal("denied request created a pending cache marker")
	} else if !errors.Is(err, redis.Nil) {
		t.Fatalf("redis error = %v, want redis.Nil after allow check denial", err)
	}
}

func TestMCP_CacheHit_IsAudited(t *testing.T) {
	_, cache := startTestRedisClient(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv, store := newIdempotencyServerWithAuthorizer(t, counter, cache, nil)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	key := "audit-hit-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": key}

	if _, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls after first = %d, want 1", counter.calls.Load())
	}

	res, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if res.IsError {
		t.Fatalf("second call error: %s", resultText(t, res))
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls after second = %d, want 1", counter.calls.Load())
	}
	records := store.Records()
	if got := len(records); got != 2 {
		t.Fatalf("audit records after cache hit = %d, want 2 (first execution + replay)", got)
	}
	// The two records must be distinguishable by action: a consumer has to be
	// able to tell the live call from the replayed one.
	if got := records[0].Action; got != sdkaudit.ActionToolExecuted {
		t.Errorf("first record action = %q, want %q", got, sdkaudit.ActionToolExecuted)
	}
	if got := records[1].Action; got != sdkaudit.ActionToolReplayed {
		t.Fatalf("replay record action = %q, want %q", got, sdkaudit.ActionToolReplayed)
	}
	if records[1].Action == sdkaudit.ActionToolExecuted {
		t.Error("cache hit must NOT emit ActionToolExecuted")
	}
	// Both records carry the key, which is covered by the chain hash.
	for i, rec := range records {
		if rec.IdempotencyKey != key {
			t.Errorf("record %d idempotency_key = %q, want %q", i, rec.IdempotencyKey, key)
		}
	}
	if err := audit.Verify(records); err != nil {
		t.Errorf("audit chain must stay valid with explicit actions: %v", err)
	}
}

func TestMCP_CacheMiss_IsAudited(t *testing.T) {
	client, cache := startTestRedisClient(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv, store := newIdempotencyServerWithAuthorizer(t, counter, cache, nil)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	key := "audit-miss-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": key}
	res, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("call error: %s", resultText(t, res))
	}
	if counter.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", counter.calls.Load())
	}
	records := store.Records()
	if got := len(records); got != 1 {
		t.Fatalf("audit records after cache miss = %d, want 1", got)
	}
	if got := records[0].Action; got != sdkaudit.ActionToolExecuted {
		t.Fatalf("miss record action = %q, want %q", got, sdkaudit.ActionToolExecuted)
	}
	if records[0].Action == sdkaudit.ActionToolReplayed {
		t.Error("cache miss must NOT emit ActionToolReplayed")
	}
	if got := records[0].IdempotencyKey; got != key {
		t.Errorf("miss record idempotency_key = %q, want %q", got, key)
	}
	// The completed entry must be readable: proves the value was cached.
	if err := client.Get(context.Background(), "idem:tool:11111111-1111-1111-1111-111111111111:"+key).Err(); err != nil {
		t.Errorf("expected a completed cache entry after a miss, got %v", err)
	}
}

// TestMCP_CorruptCache_ReExecutes pins the double-record fix: a cached entry
// whose response payload cannot be decoded must not be audited as a successful
// replay. The call falls through to a real execution, and the next retry gets a
// healthy replay.
func TestMCP_CorruptCache_ReExecutes(t *testing.T) {
	client, cache := startTestRedisClient(t)
	counter := &countingExecutor{result: map[string]any{"status": "success"}}
	srv, store := newIdempotencyServerWithAuthorizer(t, counter, cache, nil)

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	key := "corrupt-" + uuid.New().String()
	meta := map[string]any{"idempotency_key": key}
	redisKey := "idem:tool:11111111-1111-1111-1111-111111111111:" + key

	if _, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Keep the entry decodable but make the cached response undecodable as a
	// tool result object: response_json is a string, not a map.
	if err := client.Set(ctx, redisKey, `{"status":"completed","response_json":"not-an-object"}`, time.Hour).Err(); err != nil {
		t.Fatalf("corrupt seed: %v", err)
	}

	res, err := callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if res.IsError {
		t.Fatalf("second call error (corrupt cache must fail open to execution): %s", resultText(t, res))
	}
	if counter.calls.Load() != 2 {
		t.Fatalf("executor calls = %d, want 2 (corrupt entry must re-execute)", counter.calls.Load())
	}
	// Exactly one record per call: the corrupt replay must not add a second one.
	records := store.Records()
	if got := len(records); got != 2 {
		t.Fatalf("audit records = %d, want 2 (no double-record on a corrupt entry)", got)
	}
	for i, rec := range records {
		if rec.Action != sdkaudit.ActionToolExecuted {
			t.Errorf("record %d action = %q, want %q (a corrupt entry is not a replay)", i, rec.Action, sdkaudit.ActionToolExecuted)
		}
	}

	// The re-execution must have replaced the corrupt value, so a third call is
	// a normal replay rather than another execution.
	res, err = callToolWithMeta(t, srv, ctx, "git.push", map[string]any{"repo": "test"}, meta)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if res.IsError {
		t.Fatalf("third call error: %s", resultText(t, res))
	}
	if counter.calls.Load() != 2 {
		t.Errorf("executor calls after third = %d, want 2 (cache recovered)", counter.calls.Load())
	}
	records = store.Records()
	if got := len(records); got != 3 {
		t.Fatalf("audit records after third call = %d, want 3", got)
	}
	if got := records[2].Action; got != sdkaudit.ActionToolReplayed {
		t.Errorf("third record action = %q, want %q", got, sdkaudit.ActionToolReplayed)
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
