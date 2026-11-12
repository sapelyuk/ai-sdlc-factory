package agent

import (
	"errors"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

func TestBudgetGuard_Consume_OK(t *testing.T) {
	g := NewBudgetGuard(10.0, 50)

	// Consume within budget.
	_, err := g.Consume(client.LLMUsage{CostUSD: 0.5, TotalCostUSD: 0.5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Steps() != 1 {
		t.Errorf("steps = %d, want 1", g.Steps())
	}
	if g.Remaining() < 9.49 || g.Remaining() > 9.51 {
		t.Errorf("remaining = %.2f, want ~9.50", g.Remaining())
	}
}

func TestBudgetGuard_Consume_CostExceeded(t *testing.T) {
	g := NewBudgetGuard(1.0, 100)

	// Exceed cost limit.
	_, err := g.Consume(client.LLMUsage{CostUSD: 0.6, TotalCostUSD: 0.6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = g.Consume(client.LLMUsage{CostUSD: 0.5, TotalCostUSD: 1.1})
	if err == nil {
		t.Fatal("expected ErrCostExceeded")
	}
	if !errors.Is(err, ErrCostExceeded) {
		t.Fatalf("want ErrCostExceeded, got %v", err)
	}
}

func TestBudgetGuard_Consume_StepsExceeded(t *testing.T) {
	g := NewBudgetGuard(100.0, 2)

	_, _ = g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
	_, err := g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
	if err == nil {
		t.Fatal("expected ErrStepsExceeded")
	}
	if !errors.Is(err, ErrStepsExceeded) {
		t.Fatalf("want ErrStepsExceeded, got %v", err)
	}
}

func TestBudgetGuard_Consume_BothExceeded_CostFirst(t *testing.T) {
	g := NewBudgetGuard(0.1, 1)

	// This exceeds both cost and steps.
	_, err := g.Consume(client.LLMUsage{CostUSD: 0.05, TotalCostUSD: 0.05})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = g.Consume(client.LLMUsage{CostUSD: 0.05, TotalCostUSD: 0.15})
	if err == nil {
		t.Fatal("expected error")
	}
	// Steps check comes first in the code.
	if !errors.Is(err, ErrStepsExceeded) {
		t.Fatalf("want ErrStepsExceeded, got %v", err)
	}
}

func TestBudgetGuard_Snapshot_Restore(t *testing.T) {
	g := NewBudgetGuard(10.0, 50)
	_, _ = g.Consume(client.LLMUsage{TotalCostUSD: 1.0})
	_, _ = g.Consume(client.LLMUsage{TotalCostUSD: 2.0})

	snap := g.Snapshot()
	if snap.Steps != 2 {
		t.Errorf("snapshot steps = %d, want 2", snap.Steps)
	}
	if snap.TotalCost != 3.0 {
		t.Errorf("snapshot cost = %.1f, want 3.0", snap.TotalCost)
	}

	g2 := RestoreBudgetGuard(10.0, 50, snap)
	if g2.Steps() != 2 {
		t.Errorf("restored steps = %d, want 2", g2.Steps())
	}
	if g2.TotalCost() != 3.0 {
		t.Errorf("restored cost = %.1f, want 3.0", g2.TotalCost())
	}
}

func TestBudgetGuard_Consume_ZeroBudget(t *testing.T) {
	g := NewBudgetGuard(0.0, 1)

	_, err := g.Consume(client.LLMUsage{CostUSD: 0.0, TotalCostUSD: 0.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = g.Consume(client.LLMUsage{CostUSD: 0.001, TotalCostUSD: 0.001})
	if err == nil {
		t.Fatal("expected error on positive cost with zero budget")
	}
}

func TestBudgetGuard_Consume_Warning80Cost(t *testing.T) {
	g := NewBudgetGuard(10.0, 1000)

	// Below 80%: no warning.
	w, err := g.Consume(client.LLMUsage{TotalCostUSD: 5.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Fatalf("no warning expected below 80%%, got %+v", w)
	}

	// Crossing 80%: warning fires.
	w, err = g.Consume(client.LLMUsage{TotalCostUSD: 3.0}) // total 8.0 == 80%
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected cost_80 warning at 80%")
	}
	if w.Threshold != "cost_80" || w.Unit != "usd" {
		t.Errorf("warning = %+v, want cost_80/usd", w)
	}
	if w.Max != 10.0 {
		t.Errorf("warning max = %.1f, want 10.0", w.Max)
	}

	// Further spending within budget: warning must not fire again.
	w, err = g.Consume(client.LLMUsage{TotalCostUSD: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("warning must fire once, got %+v on the next consume", w)
	}
}

func TestBudgetGuard_Consume_Warning80Steps(t *testing.T) {
	g := NewBudgetGuard(1000.0, 10)

	// 7 steps: below 80%.
	for i := 0; i < 7; i++ {
		w, err := g.Consume(client.LLMUsage{TotalCostUSD: 0.1})
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if w != nil {
			t.Fatalf("step %d: no warning expected below 80%%, got %+v", i, w)
		}
	}

	// Step 8 == 80%: warning fires.
	w, err := g.Consume(client.LLMUsage{TotalCostUSD: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected steps_80 warning at 80%")
	}
	if w.Threshold != "steps_80" || w.Unit != "steps" {
		t.Errorf("warning = %+v, want steps_80/steps", w)
	}

	// Step 9: warning does not repeat.
	w, err = g.Consume(client.LLMUsage{TotalCostUSD: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("warning must fire once, got %+v", w)
	}
}

func TestBudgetGuard_Consume_HardLimitAfterWarning(t *testing.T) {
	g := NewBudgetGuard(10.0, 1000)

	// Cross 80%: warning.
	w, err := g.Consume(client.LLMUsage{TotalCostUSD: 8.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected cost_80 warning")
	}

	// Spend the remaining 20% plus more: hard stop.
	_, err = g.Consume(client.LLMUsage{TotalCostUSD: 2.5})
	if err == nil {
		t.Fatal("expected ErrCostExceeded after the warning")
	}
	if !errors.Is(err, ErrCostExceeded) {
		t.Fatalf("want ErrCostExceeded, got %v", err)
	}
}

func TestBudgetGuard_Consume_WarningSurvivesSnapshotRoundTrip(t *testing.T) {
	g := NewBudgetGuard(10.0, 1000)

	// Cross 80% on this guard instance.
	if _, err := g.Consume(client.LLMUsage{TotalCostUSD: 8.0}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The activity rebuilds the guard from the snapshot: the fired flag must
	// travel with it, otherwise every step past 80% warns again.
	snap := g.Snapshot()
	if !snap.WarnedCost {
		t.Fatal("snapshot must carry WarnedCost")
	}
	g2 := RestoreBudgetGuard(10.0, 1000, snap)
	w, err := g2.Consume(client.LLMUsage{TotalCostUSD: 0.5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("restored guard must not re-warn, got %+v", w)
	}
}

func TestBudgetGuard_Consume_HardLimitBeatsWarning(t *testing.T) {
	// A single call that jumps past the hard limit must be a terminal error,
	// not a warning, even though it also crosses 80%.
	g := NewBudgetGuard(1.0, 1000)

	w, err := g.Consume(client.LLMUsage{TotalCostUSD: 1.5})
	if err == nil {
		t.Fatal("expected ErrCostExceeded")
	}
	if !errors.Is(err, ErrCostExceeded) {
		t.Fatalf("want ErrCostExceeded, got %v", err)
	}
	if w != nil {
		t.Errorf("exceeded call must not report a warning, got %+v", w)
	}
}
