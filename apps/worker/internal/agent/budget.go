// Package agent implements the ReAct agent loop and budget guard.
package agent

import (
	"errors"
	"fmt"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

// Budget Guard errors (hard stop).
var (
	ErrCostExceeded  = errors.New("budget: max cost USD exceeded")
	ErrStepsExceeded = errors.New("budget: max steps exceeded")
)

// BudgetGuard enforces hard cost and step limits per task run.
// It does NOT support soft degradation — exceeding a limit is a terminal error.
type BudgetGuard struct {
	MaxCostUSD float64
	MaxSteps   int

	steps     int
	totalCost float64
}

// NewBudgetGuard creates a new budget guard with the given limits.
func NewBudgetGuard(maxCostUSD float64, maxSteps int) *BudgetGuard {
	return &BudgetGuard{
		MaxCostUSD: maxCostUSD,
		MaxSteps:   maxSteps,
	}
}

// Consume records usage from an LLM call and checks budget limits.
// Returns ErrStepsExceeded or ErrCostExceeded if the limit is reached.
func (b *BudgetGuard) Consume(usage client.LLMUsage) error {
	b.steps++
	b.totalCost += usage.TotalCostUSD

	if b.steps > b.MaxSteps {
		return fmt.Errorf("%w: steps=%d limit=%d", ErrStepsExceeded, b.steps, b.MaxSteps)
	}
	if b.totalCost > b.MaxCostUSD {
		return fmt.Errorf("%w: cost=%.6f limit=%.6f", ErrCostExceeded, b.totalCost, b.MaxCostUSD)
	}
	return nil
}

// Steps returns the number of steps consumed.
func (b *BudgetGuard) Steps() int {
	return b.steps
}

// TotalCost returns the accumulated cost in USD.
func (b *BudgetGuard) TotalCost() float64 {
	return b.totalCost
}

// Remaining returns the remaining budget in USD.
func (b *BudgetGuard) Remaining() float64 {
	r := b.MaxCostUSD - b.totalCost
	if r < 0 {
		return 0
	}
	return r
}

// Snapshot returns a serializable state for Temporal activity context.
type BudgetSnapshot struct {
	Steps     int
	TotalCost float64
}

// Snapshot returns the current budget state.
func (b *BudgetGuard) Snapshot() BudgetSnapshot {
	return BudgetSnapshot{
		Steps:     b.steps,
		TotalCost: b.totalCost,
	}
}

// RestoreBudgetGuard creates a BudgetGuard from a snapshot.
func RestoreBudgetGuard(maxCostUSD float64, maxSteps int, snap BudgetSnapshot) *BudgetGuard {
	return &BudgetGuard{
		MaxCostUSD: maxCostUSD,
		MaxSteps:   maxSteps,
		steps:      snap.Steps,
		totalCost:  snap.TotalCost,
	}
}
