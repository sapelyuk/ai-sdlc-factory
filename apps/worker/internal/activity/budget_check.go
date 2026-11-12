// Package activity provides the day-level budget check activity (issue #23).
package activity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BudgetDailyCheckInput is the input for the day-level budget check.
type BudgetDailyCheckInput struct {
	// WorkspaceID scopes the day spend query.
	WorkspaceID uuid.UUID `json:"workspace_id"`
	// MaxDailyUSD is the workspace budget for the current UTC calendar day.
	MaxDailyUSD float64 `json:"max_daily_usd"`
}

// BudgetDailyCheckOutput is the result of the day-level budget check.
type BudgetDailyCheckOutput struct {
	// SpentToday is the workspace cost accumulated today (UTC), in USD.
	SpentToday float64 `json:"spent_today"`
	// Exceeded is true when SpentToday reaches the day budget (hard stop).
	Exceeded bool `json:"exceeded"`
	// Warning80 is true when SpentToday crossed 80% of the day budget.
	Warning80 bool `json:"warning_80"`
}

// budgetSpendQuerier reads the day spend. It is satisfied by *pgxpool.Pool and
// by test fakes, so the activity can run without a database in unit tests.
type budgetSpendQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// BudgetDailyCheckActivity sums the workspace LLM spend of the current UTC day
// from llm_logical_calls and compares it with the day budget (NFR-9).
//
// It is a read-only check run before a chain starts: a workspace that already
// spent its day budget must not launch new work.
type BudgetDailyCheckActivity struct {
	pool budgetSpendQuerier
}

// NewBudgetDailyCheckActivity creates the activity backed by the worker pool.
// A nil pool disables the check: the activity always reports within budget,
// so a worker started without a database keeps running.
func NewBudgetDailyCheckActivity(pool *pgxpool.Pool) *BudgetDailyCheckActivity {
	if pool == nil {
		return &BudgetDailyCheckActivity{}
	}
	return &BudgetDailyCheckActivity{pool: pool}
}

// daySpendSQL sums total_cost_usd for the workspace within the current UTC
// calendar day. It uses the (workspace_id, started_at) index from migration
// 0010. total_cost_usd is the actual spend including retries (ADR-021), which
// is the value budget aggregation must use (SYSTEM_DESIGN.md §11.4).
const daySpendSQL = `
SELECT COALESCE(SUM(total_cost_usd), 0)
FROM llm_logical_calls
WHERE workspace_id = $1
  AND started_at >= date_trunc('day', now() AT TIME ZONE 'utc')
  AND started_at <  date_trunc('day', now() AT TIME ZONE 'utc') + interval '1 day'`

// Execute returns the day spend and the budget verdict for the workspace.
func (a *BudgetDailyCheckActivity) Execute(ctx context.Context, in BudgetDailyCheckInput) (*BudgetDailyCheckOutput, error) {
	out := &BudgetDailyCheckOutput{}
	if in.MaxDailyUSD <= 0 {
		// Budget disabled: nothing to enforce.
		return out, nil
	}
	if a.pool == nil {
		return out, nil
	}
	if in.WorkspaceID == uuid.Nil {
		return nil, fmt.Errorf("activity: budget daily check: workspace_id is required")
	}

	var spent float64
	// llm_logical_calls is RLS-protected, so the query must run inside a
	// transaction that sets the workspace scope (same pattern as the audit
	// and gate stores).
	type txBeginner interface {
		Begin(ctx context.Context) (pgx.Tx, error)
	}
	pool, ok := a.pool.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("activity: budget daily check: pool does not support transactions")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("activity: budget daily check: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, in.WorkspaceID.String()); err != nil {
		return nil, fmt.Errorf("activity: budget daily check: set workspace: %w", err)
	}
	if err := tx.QueryRow(ctx, daySpendSQL, in.WorkspaceID.String()).Scan(&spent); err != nil {
		return nil, fmt.Errorf("activity: budget daily check: query day spend: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("activity: budget daily check: commit: %w", err)
	}

	out.SpentToday = spent
	// A hard stop at 100%: the day budget is spent.
	out.Exceeded = spent >= in.MaxDailyUSD
	// The 80% warning is only meaningful while the budget is not exhausted.
	out.Warning80 = !out.Exceeded && spent >= 0.8*in.MaxDailyUSD
	return out, nil
}
