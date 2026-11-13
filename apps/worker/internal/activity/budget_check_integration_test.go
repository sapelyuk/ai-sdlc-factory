package activity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/testutil"
)

// TestBudgetDailyCheck_Integration verifies the SQL path of
// postgresDaySpendReader.DaySpend against a real PostgreSQL 18 + pgvector
// (NFR-9, ADR-021). Unit tests with a mocked DaySpendReader cannot catch
// RLS bugs, UTC boundary off-by-one, or schema drift (column name, index).
//
// The guard is a runtime check (testing.Short + Docker availability inside the
// helper), matching store_postgres_integration_test.go: it must NOT be a
// //go:build tag, because the CI test job runs `go test ./...` without -short,
// so the suite executes here and only skips when Docker is genuinely absent
// (and never on CI, where the helper fails the build instead).
func TestBudgetDailyCheck_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}

	// llm_logical_calls is created and RLS-enabled entirely within 0010. Its
	// workspace_id column has no foreign key, so no other migration is needed.
	pool := testutil.StartPostgres(t, "budget_check_test", []string{
		"0010_llm_gateway.up.sql",
	})

	reader := NewPostgresDaySpendReader(pool)

	// All timestamps are anchored to today's UTC midnight, not to a fixed
	// offset from time.Now(). A "now()-23h" seed would be yesterday for most
	// of the day but today between 23:00 and 24:00 UTC, making the assertions
	// depend on wall-clock time. Anchoring to midnight keeps the UTC-boundary
	// intent deterministic while still exercising the exact date_trunc logic.
	todayUTC := time.Now().UTC().Truncate(24 * time.Hour)

	t.Run("SumsTodayOnly", func(t *testing.T) {
		ws := uuid.New()
		seedLLMCall(t, pool, ws, 2.00, todayUTC.Add(1*time.Hour))   // today
		seedLLMCall(t, pool, ws, 3.00, todayUTC.Add(2*time.Hour))   // today
		seedLLMCall(t, pool, ws, 10.00, todayUTC.Add(-1*time.Hour)) // yesterday UTC

		spent, err := reader.DaySpend(context.Background(), ws)
		require.NoError(t, err)
		// The two today rows sum to $5. This excludes yesterday's $10 (a bare
		// day-truncation bug would return $15) and proves today is counted
		// (an inverted boundary would return $0).
		require.InDelta(t, 5.00, spent, 1e-6)
	})

	t.Run("RLSCrossWorkspaceIsolation", func(t *testing.T) {
		// The main fail-open prevention check. If the WHERE workspace_id = $1
		// defense-in-depth (or the set_config RLS scope) were missing, both
		// calls would return the same cross-workspace total.
		wsA, wsB := uuid.New(), uuid.New()
		seedLLMCall(t, pool, wsA, 3.00, todayUTC.Add(1*time.Hour))
		seedLLMCall(t, pool, wsB, 10.00, todayUTC.Add(1*time.Hour))

		spentA, err := reader.DaySpend(context.Background(), wsA)
		require.NoError(t, err)
		require.InDelta(t, 3.00, spentA, 1e-6, "wsA must not see wsB spend")

		spentB, err := reader.DaySpend(context.Background(), wsB)
		require.NoError(t, err)
		require.InDelta(t, 10.00, spentB, 1e-6, "wsB must not see wsA spend")
	})

	t.Run("UTCDayBoundary", func(t *testing.T) {
		ws := uuid.New()
		// 23:00 yesterday UTC: in a UTC+5 machine's local time this is already
		// "today", but the query is UTC-anchored so it must be excluded.
		seedLLMCall(t, pool, ws, 7.00, todayUTC.Add(-1*time.Hour))
		seedLLMCall(t, pool, ws, 2.00, todayUTC.Add(1*time.Hour)) // today UTC

		spent, err := reader.DaySpend(context.Background(), ws)
		require.NoError(t, err)
		require.InDelta(t, 2.00, spent, 1e-6, "only the today-UTC row counts")
	})

	t.Run("ThresholdsUnderWarningExceeded", func(t *testing.T) {
		// Exercises the full verdict path through BudgetDailyCheckActivity
		// backed by the real reader, not raw SQL.
		activity := NewBudgetDailyCheckActivity(reader)
		const dayMax = 4.00

		cases := []struct {
			name          string
			seed          float64
			wantExceeded  bool
			wantWarning80 bool
		}{
			{"under", 3.00, false, false},   // 75%  -> below the 80% warning
			{"warning", 3.50, false, true},  // 87.5% -> warning, not exceeded
			{"exceeded", 4.50, true, false}, // 112%  -> hard stop
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ws := uuid.New()
				seedLLMCall(t, pool, ws, tc.seed, todayUTC.Add(1*time.Hour))

				out, err := activity.Execute(context.Background(), BudgetDailyCheckInput{
					WorkspaceID: ws,
					MaxDailyUSD: dayMax,
				})
				require.NoError(t, err)
				require.InDelta(t, tc.seed, out.SpentToday, 1e-6)
				require.Equal(t, tc.wantExceeded, out.Exceeded,
					"exceeded for seed %.2f of %.2f", tc.seed, dayMax)
				require.Equal(t, tc.wantWarning80, out.Warning80,
					"warning_80 for seed %.2f of %.2f", tc.seed, dayMax)
			})
		}
	})
}

// seedLLMCall inserts one llm_logical_calls row for the given workspace.
// The column names come from migration 0010_llm_gateway.up.sql: the spend
// column is total_cost_usd (what daySpendSQL sums) and the timestamp column
// is started_at (NOT created_at). profile_name, model_id_used, provider_used
// and status are NOT NULL, so they are filled with valid catalog values.
// The insert runs inside a transaction that sets the workspace RLS scope,
// mirroring the production reader and staying correct whether or not the
// connection user bypasses RLS.
func seedLLMCall(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, costUSD float64, startedAt time.Time) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, wsID.String())
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO llm_logical_calls
			(workspace_id, profile_name, model_id_used, provider_used, status, total_cost_usd, started_at)
		VALUES
			($1, 'code-strong', 'claude-sonnet-4-20250101', 'anthropic', 'succeeded', $2, $3)`,
		wsID, costUSD, startedAt)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}
