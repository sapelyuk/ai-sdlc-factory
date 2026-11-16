package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

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

	container, err := rediscontainer.Run(ctx, "redis:7-alpine")
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
