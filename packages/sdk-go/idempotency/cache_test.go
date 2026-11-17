package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

// redisImage pins the same digest as deploy/compose/docker-compose.yml so the
// integration tests run against the exact image the stack ships (ADR-026).
const redisImage = "docker.io/library/redis:7-alpine@sha256:7f7d30b7fdd09467bac58198a1f166dd69789d348d4fe859cccb20e0cf287fbe"

// startRedis starts a Redis container for integration tests.
// Follows the same Docker detect guard pattern as apps/worker/internal/testutil/postgres.go.
func startRedis(t *testing.T) *redis.Client {
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

	opts, err := redis.ParseURL(connStr)
	if err != nil {
		t.Fatalf("parse URL %q: %v", connStr, err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	// Flush between tests.
	if err := client.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return client
}

func TestCache_ClaimNew(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)

	entry, hit, err := cache.Claim(context.Background(), "llm", "ws1", "key1", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if hit {
		t.Fatal("expected miss on first claim")
	}
	if entry != nil {
		t.Fatalf("expected nil entry on first claim, got %v", entry)
	}
}

func TestCache_CompleteThenClaimHit(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// First claim — acquire.
	_, hit, err := cache.Claim(ctx, "llm", "ws1", "key1", 60*time.Second)
	if err != nil || hit {
		t.Fatalf("first Claim: hit=%v err=%v", hit, err)
	}

	// Complete.
	respJSON := json.RawMessage(`{"result":"ok"}`)
	err = cache.Complete(ctx, "llm", "ws1", "key1", Entry{
		ResponseJSON:  respJSON,
		LogicalCallID: "call-123",
		ProviderUsed:  "anthropic",
		ModelUsed:     "claude-sonnet-4-20250101",
	}, 24*time.Hour)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Second claim — should hit.
	entry, hit, err := cache.Claim(ctx, "llm", "ws1", "key1", 60*time.Second)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if !hit {
		t.Fatal("expected hit after Complete")
	}
	if entry.Status != "completed" {
		t.Errorf("status = %q, want completed", entry.Status)
	}
	if entry.LogicalCallID != "call-123" {
		t.Errorf("logical_call_id = %q, want call-123", entry.LogicalCallID)
	}
	if string(entry.ResponseJSON) != `{"result":"ok"}` {
		t.Errorf("response_json = %s, want {\"result\":\"ok\"}", entry.ResponseJSON)
	}
}

func TestCache_ConcurrentClaim_ErrPending(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// First claim — acquire.
	_, hit, err := cache.Claim(ctx, "tool", "ws1", "key1", 60*time.Second)
	if err != nil || hit {
		t.Fatalf("first Claim: hit=%v err=%v", hit, err)
	}

	// Second claim while first is still pending.
	_, _, err = cache.Claim(ctx, "tool", "ws1", "key1", 60*time.Second)
	if err == nil {
		t.Fatal("expected ErrPending, got nil")
	}
	if !strings.Contains(err.Error(), "concurrent request") {
		t.Errorf("error = %v, want concurrent request message", err)
	}
}

func TestCache_WorkspaceIsolation(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// Claim in wsA.
	_, hit, err := cache.Claim(ctx, "llm", "wsA", "same-key", 60*time.Second)
	if err != nil || hit {
		t.Fatalf("wsA Claim: hit=%v err=%v", hit, err)
	}

	// Same key in wsB should succeed (different prefix).
	_, hit, err = cache.Claim(ctx, "llm", "wsB", "same-key", 60*time.Second)
	if err != nil {
		t.Fatalf("wsB Claim: %v", err)
	}
	if hit {
		t.Fatal("wsB should not hit wsA's pending marker")
	}
}

func TestCache_MarkFailed_AllowsRetry(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// Claim.
	_, hit, err := cache.Claim(ctx, "tool", "ws1", "key1", 60*time.Second)
	if err != nil || hit {
		t.Fatalf("first Claim: hit=%v err=%v", hit, err)
	}

	// MarkFailed.
	if err := cache.MarkFailed(ctx, "tool", "ws1", "key1"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Retry should succeed.
	_, hit, err = cache.Claim(ctx, "tool", "ws1", "key1", 60*time.Second)
	if err != nil {
		t.Fatalf("retry Claim after MarkFailed: %v", err)
	}
	if hit {
		t.Fatal("retry should get fresh claim, not hit")
	}
}

func TestCache_PendingTTLExpiry(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// Claim with 1s TTL.
	_, hit, err := cache.Claim(ctx, "llm", "ws1", "key1", 1*time.Second)
	if err != nil || hit {
		t.Fatalf("first Claim: hit=%v err=%v", hit, err)
	}

	// Wait for expiry.
	time.Sleep(1100 * time.Millisecond)

	// Retry should succeed (pending marker expired).
	_, hit, err = cache.Claim(ctx, "llm", "ws1", "key1", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim after TTL expiry: %v", err)
	}
	if hit {
		t.Fatal("should get fresh claim after pending expiry")
	}
}

func TestCache_NilCache_Noop(t *testing.T) {
	cache := New(nil, nil, nil)
	if cache != nil {
		t.Fatal("New(nil,...) should return nil")
	}

	ctx := context.Background()
	// All methods should not panic on nil receiver.
	entry, hit, err := cache.Claim(ctx, "llm", "ws1", "key1", 60*time.Second)
	if entry != nil || hit || err != nil {
		t.Errorf("nil Claim: entry=%v hit=%v err=%v", entry, hit, err)
	}
	if err := cache.Complete(ctx, "llm", "ws1", "key1", Entry{}, time.Hour); err != nil {
		t.Errorf("nil Complete: %v", err)
	}
	if err := cache.MarkFailed(ctx, "llm", "ws1", "key1"); err != nil {
		t.Errorf("nil MarkFailed: %v", err)
	}
}

// ── Metrics and logger coverage ────────────────────────────────

type fakeMetrics struct {
	hits     map[string]int
	misses   map[string]int
	errs     map[string]int
	pending  map[string]int
	errorOps []string
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{
		hits:    map[string]int{},
		misses:  map[string]int{},
		errs:    map[string]int{},
		pending: map[string]int{},
	}
}

func (f *fakeMetrics) Hit(ns string)        { f.hits[ns]++ }
func (f *fakeMetrics) Miss(ns string)       { f.misses[ns]++ }
func (f *fakeMetrics) ErrPending(ns string) { f.pending[ns]++ }
func (f *fakeMetrics) Error(ns, op string) {
	f.errs[ns]++
	f.errorOps = append(f.errorOps, op)
}

type fakeLogger struct {
	warnings int
}

func (f *fakeLogger) Warn(string, ...any) { f.warnings++ }

func TestNewSlogLogger_WritesWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(slog.New(slog.NewTextHandler(&buf, nil)))

	logger.Warn("cache unavailable", "namespace", "llm", "error", "boom")

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("expected WARN level, got %q", out)
	}
	if !strings.Contains(out, "cache unavailable") {
		t.Fatalf("expected message in output, got %q", out)
	}
	if !strings.Contains(out, "namespace=llm") {
		t.Fatalf("expected kv pair in output, got %q", out)
	}
}

func TestCache_ConcurrentClaimAfterExpiry_ExactlyOneWins(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	_, hit, err := cache.Claim(ctx, "llm", "ws-race", "race-key", 1*time.Millisecond)
	if err != nil || hit {
		t.Fatalf("initial claim: hit=%v err=%v", hit, err)
	}
	time.Sleep(10 * time.Millisecond)

	results := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, hit, err := cache.Claim(ctx, "llm", "ws-race", "race-key", 60*time.Second)
			switch {
			case err == nil && !hit:
				results <- "fresh"
			case errors.Is(err, ErrPending):
				results <- "pending"
			case hit:
				results <- "cached"
			default:
				results <- "error"
			}
		}()
	}
	wg.Wait()
	close(results)

	freshCount, pendingCount := 0, 0
	for r := range results {
		switch r {
		case "fresh":
			freshCount++
		case "pending":
			pendingCount++
		}
	}

	if freshCount != 1 {
		t.Fatalf("fresh winners = %d, want 1", freshCount)
	}
	if pendingCount != 1 {
		t.Fatalf("pending followers = %d, want 1", pendingCount)
	}
}

func TestCache_MetricsCallbacks(t *testing.T) {
	client := startRedis(t)
	metrics := newFakeMetrics()
	cache := New(client, nil, metrics)
	ctx := context.Background()

	// miss
	if _, _, err := cache.Claim(ctx, "llm", "ws1", "k", 60*time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if metrics.misses["llm"] != 1 {
		t.Errorf("misses = %d, want 1", metrics.misses["llm"])
	}

	// pending
	if _, _, err := cache.Claim(ctx, "llm", "ws1", "k", 60*time.Second); err == nil {
		t.Fatal("expected ErrPending")
	}
	if metrics.pending["llm"] != 1 {
		t.Errorf("pending = %d, want 1", metrics.pending["llm"])
	}

	// hit
	if err := cache.Complete(ctx, "llm", "ws1", "k", Entry{ResponseJSON: json.RawMessage(`{}`)}, time.Hour); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, hit, err := cache.Claim(ctx, "llm", "ws1", "k", 60*time.Second); err != nil || !hit {
		t.Fatalf("expected hit, got hit=%v err=%v", hit, err)
	}
	if metrics.hits["llm"] != 1 {
		t.Errorf("hits = %d, want 1", metrics.hits["llm"])
	}
}

func TestCache_RedisError_MetricsAndLogger(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	metrics := newFakeMetrics()
	logger := &fakeLogger{}
	cache := New(client, logger, metrics)
	ctx := context.Background()

	if _, _, err := cache.Claim(ctx, "llm", "ws1", "k", time.Second); err == nil {
		t.Fatal("expected error from unreachable Redis")
	}
	if metrics.errs["llm"] != 1 {
		t.Errorf("errors = %d, want 1", metrics.errs["llm"])
	}
	if len(metrics.errorOps) == 0 || metrics.errorOps[0] != "claim" {
		t.Errorf("error op = %v, want claim", metrics.errorOps)
	}
	if logger.warnings == 0 {
		t.Error("expected a WARN log on cache failure")
	}

	// Complete and MarkFailed error paths.
	if err := cache.Complete(ctx, "llm", "ws1", "k", Entry{ResponseJSON: json.RawMessage(`{}`)}, time.Hour); err == nil {
		t.Error("expected Complete error")
	}
	if err := cache.MarkFailed(ctx, "llm", "ws1", "k"); err == nil {
		t.Error("expected MarkFailed error")
	}
	if metrics.errs["llm"] != 3 {
		t.Errorf("errors = %d, want 3 (claim, complete, mark_failed)", metrics.errs["llm"])
	}
}

func TestCache_ClaimCorruptEntry(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// Pre-seed the key with invalid JSON so Claim cannot unmarshal it.
	if err := client.Set(ctx, "idem:llm:ws1:corrupt", "{not json", time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := cache.Claim(ctx, "llm", "ws1", "corrupt", time.Minute); err == nil {
		t.Fatal("expected unmarshal error for corrupt entry")
	}
}

func TestCache_CompleteInvalidResponseJSON(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	// json.RawMessage with invalid JSON makes Entry unmarshalable.
	err := cache.Complete(ctx, "llm", "ws1", "k", Entry{ResponseJSON: json.RawMessage("{bad")}, time.Hour)
	if err == nil {
		t.Fatal("expected marshal error for invalid RawMessage")
	}
}

// ── Get (authorize-before-replay read path, round-3 review) ────

func TestCache_Get_Hit(t *testing.T) {
	client := startRedis(t)
	metrics := newFakeMetrics()
	cache := New(client, nil, metrics)
	ctx := context.Background()

	seed := Entry{
		LogicalCallID: "11111111-1111-1111-1111-111111111111",
		ResponseJSON:  json.RawMessage(`{"content":"cached"}`),
		ProviderUsed:  "anthropic",
		ModelUsed:     "claude-sonnet-4-20250101",
	}
	if err := cache.Complete(ctx, "llm", "ws1", "get-hit", seed, time.Hour); err != nil {
		t.Fatalf("seed Complete: %v", err)
	}

	entry, hit, err := cache.Get(ctx, "llm", "ws1", "get-hit")
	if err != nil {
		t.Fatalf("Get: unexpected error %v", err)
	}
	if !hit {
		t.Fatal("Get: hit = false, want true for a completed entry")
	}
	if entry == nil {
		t.Fatal("Get: entry = nil on hit")
	}
	if entry.Status != "completed" {
		t.Errorf("Status = %q, want completed", entry.Status)
	}
	if entry.LogicalCallID != seed.LogicalCallID {
		t.Errorf("LogicalCallID = %q, want %q", entry.LogicalCallID, seed.LogicalCallID)
	}
	if string(entry.ResponseJSON) != string(seed.ResponseJSON) {
		t.Errorf("ResponseJSON = %s, want %s", entry.ResponseJSON, seed.ResponseJSON)
	}
	if entry.ProviderUsed != "anthropic" || entry.ModelUsed != "claude-sonnet-4-20250101" {
		t.Errorf("attribution round-trip failed: provider=%q model=%q", entry.ProviderUsed, entry.ModelUsed)
	}
	if entry.CreatedAtUnix == 0 {
		t.Error("CreatedAtUnix = 0, want Complete to stamp it")
	}
	// Get must not reserve or mutate the key: a second Get still hits.
	if _, hit, err := cache.Get(ctx, "llm", "ws1", "get-hit"); err != nil || !hit {
		t.Errorf("second Get: hit=%v err=%v, want hit and no error", hit, err)
	}
	if metrics.hits["llm"] != 2 {
		t.Errorf("hits = %d, want 2 (Get counts a hit on each completed read)", metrics.hits["llm"])
	}
}

func TestCache_Get_Miss(t *testing.T) {
	client := startRedis(t)
	metrics := newFakeMetrics()
	cache := New(client, nil, metrics)
	ctx := context.Background()

	entry, hit, err := cache.Get(ctx, "llm", "ws1", "never-written")
	if err != nil {
		t.Fatalf("Get on missing key: err = %v, want nil (miss is not an error)", err)
	}
	if hit {
		t.Error("hit = true, want false for a missing key")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil", entry)
	}
	if len(metrics.errorOps) != 0 {
		t.Errorf("errorOps = %v, want none on a plain miss", metrics.errorOps)
	}
	if metrics.hits["llm"] != 0 {
		t.Errorf("hits = %d, want 0 on a miss", metrics.hits["llm"])
	}
}

func TestCache_Get_Pending(t *testing.T) {
	client := startRedis(t)
	metrics := newFakeMetrics()
	cache := New(client, nil, metrics)
	ctx := context.Background()

	// Claim writes the in-flight pending marker; Get must surface ErrPending
	// instead of reporting a miss or a hit.
	entry, hit, err := cache.Claim(ctx, "tool", "ws1", "get-pending", time.Minute)
	if err != nil || hit || entry != nil {
		t.Fatalf("Claim: entry=%v hit=%v err=%v, want a fresh claim", entry, hit, err)
	}

	entry, hit, err = cache.Get(ctx, "tool", "ws1", "get-pending")
	if !errors.Is(err, ErrPending) {
		t.Fatalf("Get on pending marker: err = %v, want ErrPending", err)
	}
	if hit {
		t.Error("hit = true, want false for a pending marker")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil", entry)
	}
	if metrics.pending["tool"] != 1 {
		t.Errorf("pending metric = %d, want 1", metrics.pending["tool"])
	}
}

func TestCache_Get_Corrupt(t *testing.T) {
	client := startRedis(t)
	metrics := newFakeMetrics()
	logger := &fakeLogger{}
	cache := New(client, logger, metrics)
	ctx := context.Background()

	// Pre-seed invalid JSON: Get must report an error, never panic and never
	// present the value as a completed entry.
	if err := client.Set(ctx, "idem:tool:ws1:get-corrupt", "{not json", time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entry, hit, err := cache.Get(ctx, "tool", "ws1", "get-corrupt")
	if err == nil {
		t.Fatal("Get on corrupt entry: err = nil, want unmarshal error")
	}
	if hit {
		t.Error("hit = true, want false for a corrupt entry")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil", entry)
	}
	if !strings.Contains(err.Error(), "get unmarshal") {
		t.Errorf("error = %q, want it to name the get unmarshal step", err)
	}
	if len(metrics.errorOps) == 0 || metrics.errorOps[0] != "get_unmarshal" {
		t.Errorf("errorOps = %v, want get_unmarshal", metrics.errorOps)
	}
	if logger.warnings == 0 {
		t.Error("expected a WARN log on a corrupt entry")
	}
}

func TestCache_Get_NilCache(t *testing.T) {
	var cache *Cache
	ctx := context.Background()

	entry, hit, err := cache.Get(ctx, "llm", "ws1", "k")
	if err != nil {
		t.Errorf("nil Get: err = %v, want nil", err)
	}
	if hit {
		t.Error("nil Get: hit = true, want false")
	}
	if entry != nil {
		t.Errorf("nil Get: entry = %+v, want nil", entry)
	}
}

func TestCache_Get_RedisError(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	metrics := newFakeMetrics()
	logger := &fakeLogger{}
	cache := New(client, logger, metrics)
	ctx := context.Background()

	entry, hit, err := cache.Get(ctx, "llm", "ws1", "k")
	if err == nil {
		t.Fatal("Get on unreachable Redis: err = nil, want error")
	}
	if hit {
		t.Error("hit = true, want false on Redis error")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil", entry)
	}
	if len(metrics.errorOps) == 0 || metrics.errorOps[0] != "get" {
		t.Errorf("errorOps = %v, want get", metrics.errorOps)
	}
	if logger.warnings == 0 {
		t.Error("expected a WARN log on Redis failure")
	}
}

func TestCache_Get_WorkspaceIsolation(t *testing.T) {
	client := startRedis(t)
	cache := New(client, nil, nil)
	ctx := context.Background()

	const key = "shared-get-key"
	if err := cache.Complete(ctx, "tool", "ws-A", key, Entry{ResponseJSON: json.RawMessage(`{"ok":true}`)}, time.Hour); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, hit, err := cache.Get(ctx, "tool", "ws-A", key); err != nil || !hit {
		t.Fatalf("Get in ws-A: hit=%v err=%v, want hit", hit, err)
	}
	// A different workspace must not see ws-A's entry.
	entry, hit, err := cache.Get(ctx, "tool", "ws-B", key)
	if err != nil {
		t.Fatalf("Get in ws-B: err = %v, want nil (treated as a miss)", err)
	}
	if hit || entry != nil {
		t.Fatalf("Get in ws-B: hit=%v entry=%+v, want a miss (workspace isolation)", hit, entry)
	}
}
