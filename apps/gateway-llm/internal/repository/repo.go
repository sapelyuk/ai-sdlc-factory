// Package repository provides PostgreSQL data access for the LLM Gateway:
// logical calls, attempts (spend layer) and per-(provider, model) health.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
)

// Repo groups all persistence operations of the gateway.
type Repo struct {
	pool *pgxpool.Pool
}

// New creates a Repo backed by the given pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Pool exposes the underlying pool (used by the catalog and health checks).
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// ── Logical calls (RLS-protected) ─────────────────────────────

// CreateLogicalCall inserts a new logical call in 'partial' state
// (the CHECK constraint of llm_logical_calls does not allow 'in_progress';
// 'partial' marks a call that has not reached a final state yet).
// The write is detached from the caller context so a client disconnect does
// not lose the attribution row (NFR-3: cost attribution reliability).
func (r *Repo) CreateLogicalCall(ctx context.Context, call *domain.LogicalCall) error {
	ctx = context.WithoutCancel(ctx)
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		call.WorkspaceID = workspaceID
		if call.StartedAt.IsZero() {
			call.StartedAt = time.Now().UTC()
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO llm_logical_calls
				(workspace_id, run_id, task_id, agent_id, profile_name,
				 model_id_used, provider_used, status, started_at)
			VALUES ($1, $2, $3, $4, $5, '', '', 'partial', $6)
			RETURNING id`,
			workspaceID, call.RunID, call.TaskID, call.AgentID, call.ProfileName,
			call.StartedAt).Scan(&call.ID)
		if err != nil {
			return fmt.Errorf("create logical call: %w", err)
		}
		return nil
	})
}

// FinishLogicalCall writes the final state of a logical call.
// The write is detached from the caller context so a client disconnect does
// not leave the row in 'partial' state (NFR-3).
func (r *Repo) FinishLogicalCall(ctx context.Context, call *domain.LogicalCall) error {
	ctx = context.WithoutCancel(ctx)
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		tag, err := tx.Exec(ctx, `
			UPDATE llm_logical_calls SET
				model_id_used = $3, provider_used = $4, status = $5,
				attempt_count = $6, final_tokens_in = $7, final_tokens_out = $8,
				final_cost_usd = $9, total_tokens_in = $10, total_tokens_out = $11,
				total_cost_usd = $12, completed_at = $13
			WHERE id = $1 AND workspace_id = $2`,
			call.ID, workspaceID, call.ModelIDUsed, call.ProviderUsed, call.Status,
			call.AttemptCount, call.FinalTokensIn, call.FinalTokensOut,
			call.FinalCostUSD, call.TotalTokensIn, call.TotalTokensOut,
			call.TotalCostUSD, call.CompletedAt)
		if err != nil {
			return fmt.Errorf("finish logical call: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

// GetLogicalCall loads a logical call by ID within the tenant.
func (r *Repo) GetLogicalCall(ctx context.Context, id uuid.UUID) (*domain.LogicalCall, error) {
	var call domain.LogicalCall
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, _ uuid.UUID) error {
		err := tx.QueryRow(ctx, `
			SELECT id, workspace_id, run_id, task_id, agent_id, profile_name,
			       model_id_used, provider_used, status, attempt_count,
			       final_tokens_in, final_tokens_out, final_cost_usd,
			       total_tokens_in, total_tokens_out, total_cost_usd,
			       started_at, completed_at
			FROM llm_logical_calls WHERE id = $1`, id).Scan(
			&call.ID, &call.WorkspaceID, &call.RunID, &call.TaskID, &call.AgentID,
			&call.ProfileName, &call.ModelIDUsed, &call.ProviderUsed, &call.Status,
			&call.AttemptCount, &call.FinalTokensIn, &call.FinalTokensOut,
			&call.FinalCostUSD, &call.TotalTokensIn, &call.TotalTokensOut,
			&call.TotalCostUSD, &call.StartedAt, &call.CompletedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("get logical call: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &call, nil
}

// ── Attempts (spend layer) ────────────────────────────────────

// InsertAttempt records a finished HTTP attempt with its final status.
// The CHECK constraint of llm_attempts allows only final statuses, so the
// row is written once after the provider call completes.
func (r *Repo) InsertAttempt(ctx context.Context, a *domain.Attempt) error {
	if a.StartedAt.IsZero() {
		a.StartedAt = time.Now().UTC()
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO llm_attempts
			(logical_call_id, attempt_num, provider, model_id, status,
			 error_message, tokens_in, tokens_out, cost_usd, latency_ms,
			 started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		a.LogicalCallID, a.AttemptNum, a.Provider, a.ModelID, a.Status,
		a.ErrorMessage, a.TokensIn, a.TokensOut, a.CostUSD, a.LatencyMs,
		a.StartedAt, a.CompletedAt).Scan(&a.ID)
	if err != nil {
		return fmt.Errorf("insert attempt: %w", err)
	}
	return nil
}

// ListAttempts returns all attempts of a logical call, ordered by number.
func (r *Repo) ListAttempts(ctx context.Context, logicalCallID uuid.UUID) ([]domain.Attempt, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, logical_call_id, attempt_num, provider, model_id, status,
		       error_message, tokens_in, tokens_out, cost_usd, latency_ms,
		       started_at, completed_at
		FROM llm_attempts WHERE logical_call_id = $1 ORDER BY attempt_num`, logicalCallID)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()

	var out []domain.Attempt
	for rows.Next() {
		var a domain.Attempt
		if err := rows.Scan(&a.ID, &a.LogicalCallID, &a.AttemptNum, &a.Provider,
			&a.ModelID, &a.Status, &a.ErrorMessage, &a.TokensIn, &a.TokensOut,
			&a.CostUSD, &a.LatencyMs, &a.StartedAt, &a.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── Model health (global, no RLS) ─────────────────────────────

// ModelKey identifies a (provider, model_id) pair.
type ModelKey struct {
	Provider string
	ModelID  string
}

// HealthFor loads health rows for the given (provider, model) pairs.
// Missing pairs are absent from the result.
func (r *Repo) HealthFor(ctx context.Context, keys []ModelKey) (map[ModelKey]*domain.ModelHealth, error) {
	out := make(map[ModelKey]*domain.ModelHealth, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	providers := make([]string, len(keys))
	models := make([]string, len(keys))
	for i, k := range keys {
		providers[i] = k.Provider
		models[i] = k.ModelID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT provider, model_id, consecutive_failures, cooldown_until,
		       last_error_at, last_error_type, last_success_at
		FROM model_health
		WHERE provider = ANY($1) AND model_id = ANY($2)`, providers, models)
	if err != nil {
		return nil, fmt.Errorf("query model_health: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var h domain.ModelHealth
		if err := rows.Scan(&h.Provider, &h.ModelID, &h.ConsecutiveFailures,
			&h.CooldownUntil, &h.LastErrorAt, &h.LastErrorType, &h.LastSuccessAt); err != nil {
			return nil, fmt.Errorf("scan model_health: %w", err)
		}
		out[ModelKey{h.Provider, h.ModelID}] = &h
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model_health: %w", err)
	}
	// Drop rows that only matched the cross-product of the two arrays.
	for k := range out {
		found := false
		for _, want := range keys {
			if want == k {
				found = true
				break
			}
		}
		if !found {
			delete(out, k)
		}
	}
	return out, nil
}

// RecordAvailabilityFailure increments consecutive_failures and sets the
// cooldown window. Used for 429 / 5xx / timeout (ADR-021).
func (r *Repo) RecordAvailabilityFailure(ctx context.Context, key ModelKey, errType string, cooldownUntil time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO model_health
			(provider, model_id, consecutive_failures, cooldown_until, last_error_at, last_error_type)
		VALUES ($1, $2, 1, $3, now(), $4)
		ON CONFLICT (provider, model_id) DO UPDATE SET
			consecutive_failures = model_health.consecutive_failures + 1,
			cooldown_until       = EXCLUDED.cooldown_until,
			last_error_at        = now(),
			last_error_type      = EXCLUDED.last_error_type`,
		key.Provider, key.ModelID, cooldownUntil, errType)
	if err != nil {
		return fmt.Errorf("record availability failure: %w", err)
	}
	return nil
}

// RecordConfigError logs a config-class error (404/401/403/400) WITHOUT
// touching consecutive_failures or cooldown_until: such errors are not
// availability events (ADR-021).
func (r *Repo) RecordConfigError(ctx context.Context, key ModelKey, errType string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO model_health
			(provider, model_id, consecutive_failures, last_error_at, last_error_type)
		VALUES ($1, $2, 0, now(), $3)
		ON CONFLICT (provider, model_id) DO UPDATE SET
			last_error_at   = now(),
			last_error_type = EXCLUDED.last_error_type`,
		key.Provider, key.ModelID, errType)
	if err != nil {
		return fmt.Errorf("record config error: %w", err)
	}
	return nil
}

// RecordSuccess resets the failure counter and clears the cooldown.
func (r *Repo) RecordSuccess(ctx context.Context, key ModelKey) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO model_health (provider, model_id, last_success_at)
		VALUES ($1, $2, now())
		ON CONFLICT (provider, model_id) DO UPDATE SET
			consecutive_failures = 0,
			cooldown_until       = NULL,
			last_success_at      = now()`,
		key.Provider, key.ModelID)
	if err != nil {
		return fmt.Errorf("record success: %w", err)
	}
	return nil
}

// ── Transaction helper ────────────────────────────────────────

// inTenantTx begins a transaction with RLS context set.
// set_config(..., true) is semantically identical to SET LOCAL and works
// with the extended query protocol.
func inTenantTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx, uuid.UUID) error) error {
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	if err := fn(tx, workspaceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
