package gates

import (
	"context"
	"strings"
	"testing"
)

func TestAgentReviewPass(t *testing.T) {
	facts := Facts{
		EvidenceReviewApproved: true,
		EvidenceReviewAuthor:   "agent-a",
		EvidenceReviewReviewer: "agent-b",
		EvidenceReviewRound:    1,
	}
	res, err := AgentReviewGate{Name: "test", MaxRounds: 3}.Evaluate(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Errorf("expected pass, reason=%s", res.Reason)
	}
}

func TestAgentReviewNoApproval(t *testing.T) {
	facts := Facts{
		EvidenceReviewAuthor: "agent-a",
		EvidenceReviewRound:  1,
	}
	res, err := AgentReviewGate{Name: "test", MaxRounds: 3}.Evaluate(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block without approval")
	}
}

func TestAgentReviewP9Violation(t *testing.T) {
	facts := Facts{
		EvidenceReviewApproved: true,
		EvidenceReviewAuthor:   "agent-a",
		EvidenceReviewReviewer: "agent-a", // same as author
		EvidenceReviewRound:    1,
	}
	res, err := AgentReviewGate{Name: "test", MaxRounds: 3}.Evaluate(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block for P9 violation (same author/reviewer)")
	}
	if !strings.Contains(res.Reason, "P9 violation") {
		t.Errorf("reason should mention P9: %s", res.Reason)
	}
}

func TestAgentReviewMaxRounds(t *testing.T) {
	facts := Facts{
		EvidenceReviewApproved: true,
		EvidenceReviewAuthor:   "agent-a",
		EvidenceReviewReviewer: "agent-b",
		EvidenceReviewRound:    5, // exceeds max 3
	}
	res, err := AgentReviewGate{Name: "test", MaxRounds: 3}.Evaluate(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block for exceeding max_rounds")
	}
	if !strings.Contains(res.Reason, "max_rounds") {
		t.Errorf("reason should mention max_rounds: %s", res.Reason)
	}
}
