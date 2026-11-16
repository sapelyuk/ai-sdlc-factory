// Package idempotency provides a Redis-backed idempotency cache used by
// the LLM Gateway (issue #106) and Tool Gateway (issue #106). See ADR-026
// for the design rationale.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache is a Redis-backed idempotency cache. A nil *Cache is valid and
// disables caching (fail-open): all operations are no-ops. This lets the
// gateway start even when Redis is unreachable at boot.
type Cache struct {
	client  *redis.Client
	logger  Logger
	metrics Metrics
}

// Logger is the minimal logging surface used by the cache. Adapters in
// apps/*/internal/telemetry/ implement it.
type Logger interface {
	Warn(msg string, kv ...any)
}

// Metrics is the minimal metrics surface used by the cache.
type Metrics interface {
	Hit(namespace string)
	Miss(namespace string)
	Error(namespace string, op string)
	ErrPending(namespace string)
}

// New creates a Cache backed by client. If client is nil, a nil Cache is
// returned (all operations are no-ops). Never fails at construction —
// connection errors surface on first command and are handled fail-open.
func New(client *redis.Client, logger Logger, metrics Metrics) *Cache {
	if client == nil {
		return nil
	}
	return &Cache{client: client, logger: logger, metrics: metrics}
}

// ErrPending is returned by Claim when a concurrent request holds the
// in-flight marker. The caller should return 409 to its upstream.
var ErrPending = errors.New("idempotency: concurrent request with same key in flight")

// Entry is a cached value.
type Entry struct {
	Status        string          `json:"status"`          // "completed"
	LogicalCallID string          `json:"logical_call_id"` // for LLM; "" for Tool
	ResponseJSON  json.RawMessage `json:"response_json"`
	ProviderUsed  string          `json:"provider_used,omitempty"`
	ModelUsed     string          `json:"model_used,omitempty"`
	CreatedAtUnix int64           `json:"created_at_unix"`
}

// Claim atomically reserves a slot for idempotencyKey. Returns:
//   - (entry, true, nil): existing completed entry — caller must return cached response.
//   - (nil, false, nil): claim acquired — caller must invoke upstream then call Complete.
//   - (nil, false, ErrPending): concurrent in-flight request.
//   - (nil, false, err): Redis error (caller should fail-open and proceed).
func (c *Cache) Claim(ctx context.Context, namespace, workspaceID, key string, pendingTTL time.Duration) (*Entry, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	redisKey := c.redisKey(namespace, workspaceID, key)
	const maxClaimAttempts = 2
	for attempt := 1; attempt <= maxClaimAttempts; attempt++ {
		pendingValue := `{"status":"pending"}`
		ok, err := c.client.SetNX(ctx, redisKey, pendingValue, pendingTTL).Result()
		if err != nil {
			c.metricsError(namespace, "claim")
			c.warn("idempotency cache claim failed; proceeding without cache", "namespace", namespace, "error", err)
			return nil, false, fmt.Errorf("idempotency: claim: %w", err)
		}
		if ok {
			// We won the claim — fresh call.
			c.metricsMiss(namespace)
			return nil, false, nil
		}

		// Key exists: GET to distinguish pending from completed.
		raw, err := c.client.Get(ctx, redisKey).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Race: between SetNX and Get, the pending marker expired. Retry the
				// claim atomically. Treating redis.Nil as fresh without owning the
				// claim lets two concurrent callers both execute the upstream call.
				continue
			}
			c.metricsError(namespace, "claim_get")
			c.warn("idempotency cache get failed; proceeding without cache", "namespace", namespace, "error", err)
			return nil, false, fmt.Errorf("idempotency: claim get: %w", err)
		}
		var entry Entry
		if err := json.Unmarshal(raw, &entry); err != nil {
			c.metricsError(namespace, "claim_unmarshal")
			c.warn("idempotency cache entry is corrupt; proceeding without cache", "namespace", namespace, "error", err)
			return nil, false, fmt.Errorf("idempotency: claim unmarshal: %w", err)
		}
		if entry.Status == "completed" {
			c.metricsHit(namespace)
			return &entry, true, nil
		}
		// status == "pending"
		c.metricsErrPending(namespace)
		return nil, false, ErrPending
	}

	c.metricsError(namespace, "claim_race_exhausted")
	return nil, false, fmt.Errorf("idempotency: claim race exhausted after %d attempts", maxClaimAttempts)
}

// Complete stores the upstream response for a claimed key.
// Called after provider success, before returning to caller.
func (c *Cache) Complete(ctx context.Context, namespace, workspaceID, key string, entry Entry, completedTTL time.Duration) error {
	if c == nil {
		return nil
	}
	entry.Status = "completed"
	if entry.CreatedAtUnix == 0 {
		entry.CreatedAtUnix = time.Now().Unix()
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		c.metricsError(namespace, "complete_marshal")
		return fmt.Errorf("idempotency: marshal complete: %w", err)
	}
	err = c.client.Set(ctx, c.redisKey(namespace, workspaceID, key), raw, completedTTL).Err()
	if err != nil {
		c.metricsError(namespace, "complete")
		c.warn("idempotency cache write failed; response still returned", "namespace", namespace, "error", err)
	}
	return err
}

// MarkFailed deletes the pending marker so a retry can start over immediately.
// Caller uses this on provider error (before returning the error upstream).
func (c *Cache) MarkFailed(ctx context.Context, namespace, workspaceID, key string) error {
	if c == nil {
		return nil
	}
	err := c.client.Del(ctx, c.redisKey(namespace, workspaceID, key)).Err()
	if err != nil {
		c.metricsError(namespace, "mark_failed")
		c.warn("idempotency cache delete failed", "namespace", namespace, "error", err)
	}
	return err
}

func (c *Cache) redisKey(namespace, workspaceID, key string) string {
	return fmt.Sprintf("idem:%s:%s:%s", namespace, workspaceID, key)
}

func (c *Cache) metricsHit(ns string) {
	if c.metrics != nil {
		c.metrics.Hit(ns)
	}
}
func (c *Cache) metricsMiss(ns string) {
	if c.metrics != nil {
		c.metrics.Miss(ns)
	}
}
func (c *Cache) metricsErrPending(ns string) {
	if c.metrics != nil {
		c.metrics.ErrPending(ns)
	}
}
func (c *Cache) metricsError(ns, op string) {
	if c.metrics != nil {
		c.metrics.Error(ns, op)
	}
}

func (c *Cache) warn(msg string, kv ...any) {
	if c.logger != nil {
		c.logger.Warn(msg, kv...)
	}
}
