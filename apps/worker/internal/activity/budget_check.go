package activity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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

// DaySpendReader returns the LLM spend of a workspace for the current UTC
// calendar day. It is implemented by NewPostgresDaySpendReader and by test
// fakes, so the activity can be unit-tested without a database.
type DaySpendReader interface {
	DaySpend(ctx context.Context, workspaceID uuid.UUID) (float64, error)
}

// BudgetDailyCheckActivity compares the workspace LLM spend of the current UTC
// day with the day budget (NFR-9).
//
// It is a read-only check run before a chain starts: a workspace that already
// spent its day budget must not launch new work.
type BudgetDailyCheckActivity struct {
	reader DaySpendReader
}

// NewBudgetDailyCheckActivity creates the activity backed by reader. A nil
// reader disables the check: the activity always reports within budget, so a
// worker started without a database keeps running.
func NewBudgetDailyCheckActivity(reader DaySpendReader) *BudgetDailyCheckActivity {
	return &BudgetDailyCheckActivity{reader: reader}
}

// Execute returns the day spend and the budget verdict for the workspace.
func (a *BudgetDailyCheckActivity) Execute(ctx context.Context, in BudgetDailyCheckInput) (*BudgetDailyCheckOutput, error) {
	out := &BudgetDailyCheckOutput{}
	if in.MaxDailyUSD <= 0 {
		// Budget disabled: nothing to enforce.
		return out, nil
	}
	if a.reader == nil {
		return out, nil
	}
	if in.WorkspaceID == uuid.Nil {
		return nil, fmt.Errorf("activity: budget daily check: workspace_id is required")
	}

	spent, err := a.reader.DaySpend(ctx, in.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("activity: budget daily check: %w", err)
	}

	out.SpentToday = spent
	// A hard stop at 100%: the day budget is spent.
	out.Exceeded = spent >= in.MaxDailyUSD
	// The 80% warning is only meaningful while the budget is not exhausted.
	out.Warning80 = !out.Exceeded && spent >= 0.8*in.MaxDailyUSD
	return out, nil
}

// postgresDaySpendReader reads the day spend from llm_logical_calls.
type postgresDaySpendReader struct {
	pool *pgxpool.Pool
}

// NewPostgresDaySpendReader creates the production DaySpendReader. A nil pool
// yields a nil reader, which disables the activity check.
func NewPostgresDaySpendReader(pool *pgxpool.Pool) DaySpendReader {
	if pool == nil {
		return nil
	}
	return &postgresDaySpendReader{pool: pool}
}

// daySpendSQL sums total_cost_usd for the workspace within the current UTC
// calendar day. It uses the (workspace_id, started_at) index from migration
// 0010. total_cost_usd is the actual spend including retries (ADR-021), which
// is the value budget aggregation must use (SYSTEM_DESIGN.md §11.4).
//
// The day boundary is built in UTC and converted back to timestamptz: a bare
// timestamp comparison would be interpreted in the session time zone.
const daySpendSQL = `
SELECT COALESCE(SUM(total_cost_usd), 0)
FROM llm_logical_calls
WHERE workspace_id = $1
  AND started_at >= (date_trunc('day', now() AT TIME ZONE 'utc') AT TIME ZONE 'utc')
  AND started_at <  (date_trunc('day', now() AT TIME ZONE 'utc') AT TIME ZONE 'utc') + interval '1 day'`

// DaySpend implements DaySpendReader.
func (r *postgresDaySpendReader) DaySpend(ctx context.Context, workspaceID uuid.UUID) (float64, error) {
	// llm_logical_calls is RLS-protected, so the query must run inside a
	// transaction that sets the workspace scope (same pattern as the audit
	// and gate stores).
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return 0, fmt.Errorf("set workspace: %w", err)
	}
	var spent float64
	if err := tx.QueryRow(ctx, daySpendSQL, workspaceID).Scan(&spent); err != nil {
		return 0, fmt.Errorf("query day spend: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return spent, nil
}
