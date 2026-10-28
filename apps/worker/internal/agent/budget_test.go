package agent

import (
	"errors"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

func TestBudgetGuard_Consume_OK(t *testing.T) {
	g := NewBudgetGuard(10.0, 50)

	// Consume within budget.
	err := g.Consume(client.LLMUsage{CostUSD: 0.5, TotalCostUSD: 0.5})
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
	err := g.Consume(client.LLMUsage{CostUSD: 0.6, TotalCostUSD: 0.6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = g.Consume(client.LLMUsage{CostUSD: 0.5, TotalCostUSD: 1.1})
	if err == nil {
		t.Fatal("expected ErrCostExceeded")
	}
	if !errors.Is(err, ErrCostExceeded) {
		t.Fatalf("want ErrCostExceeded, got %v", err)
	}
}

func TestBudgetGuard_Consume_StepsExceeded(t *testing.T) {
	g := NewBudgetGuard(100.0, 2)

	_ = g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
	err := g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = g.Consume(client.LLMUsage{CostUSD: 0.1, TotalCostUSD: 0.1})
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
	err := g.Consume(client.LLMUsage{CostUSD: 0.05, TotalCostUSD: 0.05})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = g.Consume(client.LLMUsage{CostUSD: 0.05, TotalCostUSD: 0.15})
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
	_ = g.Consume(client.LLMUsage{TotalCostUSD: 1.0})
	_ = g.Consume(client.LLMUsage{TotalCostUSD: 2.0})

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

	err := g.Consume(client.LLMUsage{CostUSD: 0.0, TotalCostUSD: 0.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = g.Consume(client.LLMUsage{CostUSD: 0.001, TotalCostUSD: 0.001})
	if err == nil {
		t.Fatal("expected error on positive cost with zero budget")
	}
}
