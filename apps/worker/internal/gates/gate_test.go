package gates

import (
	"testing"
)

func TestGateTypeValid(t *testing.T) {
	tests := []struct {
		input    GateType
		expected bool
	}{
		{GateTypeAuto, true},
		{GateTypeAgentReview, true},
		{GateTypeHumanApproval, true},
		{GateType("unknown"), false},
		{GateType(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			if got := tt.input.Valid(); got != tt.expected {
				t.Errorf("valid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGateTypeRequiresApproval(t *testing.T) {
	if GateTypeAuto.RequiresApproval() {
		t.Error("auto should not require approval")
	}
	if GateTypeAgentReview.RequiresApproval() {
		t.Error("agent_review should not require approval")
	}
	if !GateTypeHumanApproval.RequiresApproval() {
		t.Error("human_approval must require approval")
	}
}

func TestResultPredicates(t *testing.T) {
	passed := Result{Approved: true}
	if !passed.IsPassed() {
		t.Error("expected IsPassed")
	}
	if passed.IsBlocked() {
		t.Error("expected not blocked")
	}
	if passed.IsPending() {
		t.Error("expected not pending")
	}

	rejected := Result{Rejected: true, Reason: "no"}
	if !rejected.IsBlocked() {
		t.Error("expected blocked")
	}
	if rejected.IsPassed() {
		t.Error("expected not passed")
	}
	if rejected.IsPending() {
		t.Error("expected not pending")
	}

	expired := Result{TimeoutExpired: true}
	if !expired.IsBlocked() {
		t.Error("expected blocked")
	}
	if expired.IsPassed() {
		t.Error("expected not passed")
	}
	if expired.IsPending() {
		t.Error("expected not pending")
	}

	pending := Result{}
	if !pending.IsPending() {
		t.Error("expected pending")
	}
	if pending.IsPassed() {
		t.Error("expected not passed")
	}
	if pending.IsBlocked() {
		t.Error("expected not blocked")
	}
}

func TestFactsGetters(t *testing.T) {
	f := Facts{
		"b": true,
		"n": 42,
		"s": "hello",
		"f": 3.14,
	}
	if !f.GetBool("b") {
		t.Error("expected true")
	}
	if f.GetBool("missing") {
		t.Error("expected false for missing")
	}
	if f.GetInt("n") != 42 {
		t.Errorf("expected 42, got %d", f.GetInt("n"))
	}
	if f.GetInt("missing") != 0 {
		t.Error("expected 0 for missing")
	}
	if f.GetString("s") != "hello" {
		t.Errorf("expected 'hello', got %q", f.GetString("s"))
	}
	if f.GetString("missing") != "" {
		t.Error("expected '' for missing")
	}
	if f.GetFloat64("f") != 3.14 {
		t.Errorf("expected 3.14, got %f", f.GetFloat64("f"))
	}
	if f.GetFloat64("missing") != 0 {
		t.Error("expected 0 for missing")
	}
}

func TestUnknownGateError(t *testing.T) {
	e := UnknownGateTypeError{Name: "foo", Type: "bar"}
	if got := e.Error(); got != `gates: gate "foo" has unknown type "bar"` {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestMissingConditionError(t *testing.T) {
	e := MissingConditionError{Name: "foo"}
	if got := e.Error(); got != `gates: auto gate "foo" has no registered condition` {
		t.Errorf("unexpected error: %s", got)
	}
}
