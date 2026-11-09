package gates

import (
	"context"
	"fmt"
)

// DefaultMaxReviewRounds bounds the review loop circuit breaker
// (SDLC_STATE_MACHINE.md section 7: max_rounds in review loop = 3).
const DefaultMaxReviewRounds = 3

// AgentReviewGate implements the "agent_review" gate type: approval from an
// independent reviewer agent is required (P9: verification must be
// independent of the author).
type AgentReviewGate struct {
	// Name is the gate name from the registry.
	Name string
	// MaxRounds overrides the default review loop circuit breaker (0 = default).
	MaxRounds int
}

// Evaluate checks that a reviewer agent (not the author) approved the package
// and that the review round has not exceeded the circuit breaker.
func (g AgentReviewGate) Evaluate(_ context.Context, facts Facts) (Result, error) {
	maxRounds := g.MaxRounds
	if maxRounds <= 0 {
		maxRounds = DefaultMaxReviewRounds
	}

	author := facts.GetString(EvidenceReviewAuthor)
	reviewer := facts.GetString(EvidenceReviewReviewer)

	if reviewer == "" {
		return Result{Reason: fmt.Sprintf("gate %q: no reviewer approval", g.Name)}, nil
	}

	// P9: the reviewer must be independent of the author.
	if author != "" && reviewer == author {
		return Result{
			Rejected: true,
			Reason:   fmt.Sprintf("gate %q: reviewer %q is the author (P9 violation)", g.Name, author),
		}, nil
	}

	// Circuit breaker: the review round must not exceed max_rounds.
	round := facts.GetInt(EvidenceReviewRound)
	if round > maxRounds {
		return Result{
			Rejected: true,
			Reason: fmt.Sprintf("gate %q: review round %d exceeds max_rounds %d",
				g.Name, round, maxRounds),
		}, nil
	}

	if !facts.GetBool(EvidenceReviewApproved) {
		return Result{Reason: fmt.Sprintf("gate %q: reviewer has not approved", g.Name)}, nil
	}

	return Result{Approved: true}, nil
}
