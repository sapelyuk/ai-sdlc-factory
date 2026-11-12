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
// At 80% of either limit it emits a single warning (NFR-9).
type BudgetGuard struct {
	MaxCostUSD float64
	MaxSteps   int

	steps     int
	totalCost float64
	warnCost  bool // 80% cost warning already fired
	warnSteps bool // 80% steps warning already fired
}

// BudgetWarning is emitted once when usage crosses the 80% threshold.
type BudgetWarning struct {
	// Threshold is either "cost_80" or "steps_80".
	Threshold string
	// Current is the value that crossed the threshold.
	Current float64
	// Max is the limit.
	Max float64
	// Unit is "usd" or "steps".
	Unit string
}

// NewBudgetGuard creates a new budget guard with the given limits.
func NewBudgetGuard(maxCostUSD float64, maxSteps int) *BudgetGuard {
	return &BudgetGuard{
		MaxCostUSD: maxCostUSD,
		MaxSteps:   maxSteps,
	}
}

// Consume records usage from an LLM call and checks budget limits. It returns:
//   - (nil, nil): within budget and below the 80% threshold.
//   - (*BudgetWarning, nil): within budget but the 80% threshold was crossed.
//     Each warning fires at most once; the fired flags travel in the snapshot.
//   - (nil, error): a hard limit is exceeded (terminal, checked first).
func (b *BudgetGuard) Consume(usage client.LLMUsage) (*BudgetWarning, error) {
	b.steps++
	b.totalCost += usage.TotalCostUSD

	// Hard limits first: exceeding a limit is terminal. The steps check comes
	// first to keep the original precedence when both limits break at once.
	if b.steps > b.MaxSteps {
		return nil, fmt.Errorf("%w: steps=%d limit=%d", ErrStepsExceeded, b.steps, b.MaxSteps)
	}
	if b.totalCost > b.MaxCostUSD {
		return nil, fmt.Errorf("%w: cost=%.6f limit=%.6f", ErrCostExceeded, b.totalCost, b.MaxCostUSD)
	}

	// 80% soft warnings (NFR-9): each threshold fires once per guard lifetime.
	if b.MaxCostUSD > 0 && !b.warnCost && b.totalCost >= 0.8*b.MaxCostUSD {
		b.warnCost = true
		return &BudgetWarning{Threshold: "cost_80", Current: b.totalCost, Max: b.MaxCostUSD, Unit: "usd"}, nil
	}
	if b.MaxSteps > 0 && !b.warnSteps && float64(b.steps) >= 0.8*float64(b.MaxSteps) {
		b.warnSteps = true
		return &BudgetWarning{Threshold: "steps_80", Current: float64(b.steps), Max: float64(b.MaxSteps), Unit: "steps"}, nil
	}
	return nil, nil
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
// The warning flags are part of the state: the guard is rebuilt from the
// snapshot on every activity, so "fire once" must survive the round trip.
type BudgetSnapshot struct {
	Steps       int
	TotalCost   float64
	WarnedCost  bool
	WarnedSteps bool
}

// Snapshot returns the current budget state.
func (b *BudgetGuard) Snapshot() BudgetSnapshot {
	return BudgetSnapshot{
		Steps:       b.steps,
		TotalCost:   b.totalCost,
		WarnedCost:  b.warnCost,
		WarnedSteps: b.warnSteps,
	}
}

// RestoreBudgetGuard creates a BudgetGuard from a snapshot.
func RestoreBudgetGuard(maxCostUSD float64, maxSteps int, snap BudgetSnapshot) *BudgetGuard {
	return &BudgetGuard{
		MaxCostUSD: maxCostUSD,
		MaxSteps:   maxSteps,
		steps:      snap.Steps,
		totalCost:  snap.TotalCost,
		warnCost:   snap.WarnedCost,
		warnSteps:  snap.WarnedSteps,
	}
}
