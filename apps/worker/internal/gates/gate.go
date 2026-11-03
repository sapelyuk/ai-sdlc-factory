// Package gates implements SDLC quality gates that control state transitions
// and enforce the autonomy levels R0-R3 (ADR-013).
//
// Three gate types are defined by SDLC_STATE_MACHINE.md section 4:
//
//   - auto: a deterministic condition over evidence facts; the transition is
//     allowed when the condition holds;
//   - agent_review: an approval from an independent reviewer agent is required
//     (P9: verification must be independent of the author);
//   - human_approval: an explicit decision by a human owner is required (R3,
//     ADR-013). Requests live in the approval queue and expire at their
//     deadline: an unanswered request blocks the transition.
//
// Evaluation is a pure function of the gate definition and the evidence facts,
// except for human_approval, which reads the current status of a persisted
// approval request. This keeps the gates unit-testable and safe to call from
// deterministic Temporal workflow code.
package gates

import (
	"context"
	"fmt"
	"time"
)

// GateType selects the evaluation strategy of a gate.
type GateType string

// Gate types from SDLC_STATE_MACHINE.md section 4.
const (
	GateTypeAuto          GateType = "auto"
	GateTypeAgentReview   GateType = "agent_review"
	GateTypeHumanApproval GateType = "human_approval"
)

// Valid reports whether the value is one of the three gate types.
func (t GateType) Valid() bool {
	switch t {
	case GateTypeAuto, GateTypeAgentReview, GateTypeHumanApproval:
		return true
	}
	return false
}

// RequiresApproval reports whether the gate waits on a decision recorded in
// the approval queue. Only human_approval does: agent_review approvals travel
// with the handoff package, not the queue.
func (t GateType) RequiresApproval() bool {
	return t == GateTypeHumanApproval
}

// Gate is the definition of one quality gate.
type Gate struct {
	// Name is the stable identifier, e.g. "ci_green"
	// (SDLC_STATE_MACHINE.md section 4).
	Name string
	// Type selects the evaluation strategy.
	Type GateType
	// Description documents where the gate is applied and its criterion.
	Description string
	// ApprovalTimeout bounds how long a human_approval request stays open.
	// Zero means DefaultApprovalTimeout. Ignored by other gate types.
	ApprovalTimeout time.Duration
}

// Result is the outcome of evaluating a gate.
type Result struct {
	// Approved is true when the transition is allowed.
	Approved bool
	// Rejected is true when the gate was explicitly declined (a human or a
	// reviewer said no).
	Rejected bool
	// TimeoutExpired is true when a human_approval request passed its deadline
	// without a decision. The transition is blocked: SDLC_STATE_MACHINE.md
	// section 7 does not perform the action when nobody answered in time.
	TimeoutExpired bool
	// Reason explains a non-approved outcome. It is stored in the audit event
	// so the owner sees why the chain stopped.
	Reason string
}

// IsPassed reports whether the transition may proceed.
func (r Result) IsPassed() bool {
	return r.Approved && !r.Rejected && !r.TimeoutExpired
}

// IsBlocked reports whether the transition is denied by an explicit decision
// or an expired deadline (blocking conditions of SDLC_STATE_MACHINE.md
// section 5).
func (r Result) IsBlocked() bool {
	return r.Rejected || r.TimeoutExpired
}

// IsPending reports whether the gate is neither passed nor blocked: a
// human_approval request is still open and the workflow must keep waiting.
func (r Result) IsPending() bool {
	return !r.Approved && !r.Rejected && !r.TimeoutExpired && r.Reason == ""
}

// Evaluator checks one gate against the evidence facts.
type Evaluator interface {
	Evaluate(ctx context.Context, facts Facts) (Result, error)
}

// Facts is the evidence a gate is evaluated against. Values are primitives
// (bool, string, int, float64) so they survive JSON serialization across the
// Temporal boundary.
type Facts map[string]any

// GetBool reads a boolean fact, false when absent or of another type.
func (f Facts) GetBool(key string) bool {
	v, ok := f[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// GetString reads a string fact, "" when absent or of another type.
func (f Facts) GetString(key string) string {
	v, ok := f[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetInt reads an integer fact, 0 when absent or not numeric.
func (f Facts) GetInt(key string) int {
	switch n := f[key].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// GetFloat64 reads a float fact, 0 when absent or not numeric.
func (f Facts) GetFloat64(key string) float64 {
	switch n := f[key].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// ApprovalSignalName is the Temporal signal that delivers a human decision to
// a workflow waiting on a human_approval gate.
const ApprovalSignalName = "approval_decision"

// ApprovalDecision is the payload of ApprovalSignalName. It mirrors the API
// request body so the approval queue and the workflow speak the same shape.
type ApprovalDecision struct {
	// ApprovalID identifies the request the decision belongs to. A workflow
	// waiting for several requests ignores decisions for other IDs.
	ApprovalID string `json:"approval_id"`
	// Approved is true for approve, false for reject.
	Approved bool `json:"approved"`
	// Reason carries the owner comment (rejection rationale, approval note).
	Reason string `json:"reason,omitempty"`
	// DecidedBy is the human who decided, for the audit trail.
	DecidedBy string `json:"decided_by,omitempty"`
}

// UnknownGateTypeError is returned when a gate definition declares a type the
// service cannot evaluate.
type UnknownGateTypeError struct {
	Name string
	Type GateType
}

func (e UnknownGateTypeError) Error() string {
	return fmt.Sprintf("gates: gate %q has unknown type %q", e.Name, e.Type)
}

// MissingConditionError is returned when an auto gate has no registered
// condition. An auto gate without a check must fail loudly: silently passing
// an unverified condition is the failure mode the gates exist to prevent.
type MissingConditionError struct {
	Name string
}

func (e MissingConditionError) Error() string {
	return fmt.Sprintf("gates: auto gate %q has no registered condition", e.Name)
}
