// Gate workflow helpers (issue #20, SDLC_STATE_MACHINE.md §4).
//
// The gate helper lives in the workflow package because it orchestrates
// Temporal primitives (signals, timers, selectors) that must be inside the
// workflow code — gate evaluation logic itself lives in the gates package.
package workflow

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
)

// WaitGateInput carries the parameters for a gate wait.
type WaitGateInput struct {
	// WorkspaceID scopes the approval request.
	WorkspaceID uuid.UUID
	// Name is the gate identifier from the registry (e.g. "ci_green").
	Name string
	// Facts are the evidence the gate is evaluated against.
	Facts gates.Facts
	// Timeout is the human_approval deadline. Zero means DefaultApprovalTimeout.
	Timeout time.Duration
	// RequestedBy is the human who triggered the gate (owner).
	RequestedBy uuid.UUID
	// RiskLevel pins the autonomy level (ADR-013).
	RiskLevel string
	// WorkflowID is the Temporal workflow ID.
	WorkflowID string
	// TaskRunID, StageRunID, RunID are optional references for the audit trail.
	TaskRunID  *uuid.UUID
	StageRunID *uuid.UUID
	RunID      *uuid.UUID
}

// GateOutcome is the result of waiting for a gate to be satisfied.
type GateOutcome struct {
	// Passed is true when the gate allows the transition.
	Passed bool
	// Rejected is true when the gate was explicitly declined.
	Rejected bool
	// Timeout is true when a human_approval request passed its deadline
	// without a decision.
	Timeout bool
	// Reason explains a non-passed outcome.
	Reason string
	// ApprovalID is the request ID (only for human_approval gates).
	ApprovalID string
}

// IsPassed reports whether the transition may proceed.
func (g GateOutcome) IsPassed() bool { return g.Passed }

// IsBlocked reports whether the transition is denied.
func (g GateOutcome) IsBlocked() bool { return g.Rejected || g.Timeout }

// approvalPollInterval is how often the workflow polls the approval store
// while waiting for a human decision. The signal channel is the primary path;
// the poll is a fallback for the case when the API does not send a signal.
const approvalPollInterval = 30 * time.Second

// WaitGate evaluates a quality gate and waits for the result.
//
// For auto and agent_review gates it runs a single GateCheck activity.
// For human_approval gates it creates an approval request, waits for a signal
// or the deadline, polls for the decision, then returns the outcome.
func WaitGate(ctx workflow.Context, in WaitGateInput) (GateOutcome, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// ── 1. Initial gate check ──────────────────────────────────────
	out, err := checkGate(ctx, in.Name, in.Facts)
	if err != nil {
		return GateOutcome{}, fmt.Errorf("wait gate: initial check: %w", err)
	}
	if out.Approved {
		return GateOutcome{Passed: true}, nil
	}
	if out.Rejected || out.Timeout {
		return GateOutcome{
			Rejected: out.Rejected, Timeout: out.Timeout, Reason: out.Reason,
		}, nil
	}
	// Gate is pending: need to determine if an approval mechanism exists.
	if out.GateType != string(gates.GateTypeHumanApproval) {
		// A non-human gate that is neither passed nor pending is a failed
		// condition: propagate the activity reason.
		reason := out.Reason
		if reason == "" {
			reason = fmt.Sprintf("gate %q evidence is not satisfied", in.Name)
		}
		return GateOutcome{Rejected: true, Reason: reason}, nil
	}

	// ── 2. human_approval: create the request ──────────────────────
	created, err := createApproval(ctx, in)
	if err != nil {
		return GateOutcome{}, fmt.Errorf("wait gate: create approval: %w", err)
	}

	// ── 3. Wait for the decision ───────────────────────────────────
	timeout := in.Timeout
	if timeout == 0 {
		timeout = gates.DefaultApprovalTimeout
	}
	ch := workflow.GetSignalChannel(ctx, gates.ApprovalSignalName)
	deadlineTimer := workflow.NewTimer(ctx, timeout)
	pollTimer := workflow.NewTimer(ctx, approvalPollInterval)

	for {
		sel := workflow.NewSelector(ctx)

		// ── Signal: decision arrived via Temporal signal ──────────
		var signaled bool
		var decision gates.ApprovalDecision
		sel.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, &decision)
			if decision.ApprovalID == created.ApprovalID {
				signaled = true
			}
		})

		// ── Deadline timer ───────────────────────────────────────
		var firedDeadline bool
		sel.AddFuture(deadlineTimer, func(workflow.Future) {
			firedDeadline = true
		})

		// ── Poll timer ───────────────────────────────────────────
		var firedPoll bool
		sel.AddFuture(pollTimer, func(workflow.Future) {
			firedPoll = true
		})

		sel.Select(ctx)

		// Signal for our request arrived.
		if signaled {
			// Persist the decision so the approval queue reflects it.
			decideErr := decideApproval(ctx, in.WorkspaceID, created.ApprovalID, decision.Approved, decision.DecidedBy, decision.Reason)
			if decideErr != nil {
				// The decision was already recorded by the API: ignore.
				// A real DB error is transient: continue waiting.
				_ = decideErr
			}
			if decision.Approved {
				return GateOutcome{Passed: true, ApprovalID: created.ApprovalID}, nil
			}
			return GateOutcome{Rejected: true, Reason: decision.Reason, ApprovalID: created.ApprovalID}, nil
		}

		// Deadline expired.
		if firedDeadline {
			_ = expireApproval(ctx, in.WorkspaceID, created.ApprovalID)
			return GateOutcome{Timeout: true, Reason: "approval deadline exceeded", ApprovalID: created.ApprovalID}, nil
		}

		// Poll fired: check DB for a decision.
		if firedPoll {
			status, reason, pollErr := pollApproval(ctx, in.WorkspaceID, created.ApprovalID)
			if pollErr != nil {
				// Transient DB error: keep waiting.
				pollTimer = workflow.NewTimer(ctx, approvalPollInterval)
				continue
			}
			switch gates.ApprovalStatus(status) {
			case gates.ApprovalApproved:
				return GateOutcome{Passed: true, ApprovalID: created.ApprovalID}, nil
			case gates.ApprovalRejected:
				return GateOutcome{Rejected: true, Reason: reason, ApprovalID: created.ApprovalID}, nil
			case gates.ApprovalExpired:
				return GateOutcome{Timeout: true, Reason: "approval deadline exceeded", ApprovalID: created.ApprovalID}, nil
			default:
				// Still pending: reset poll timer.
				pollTimer = workflow.NewTimer(ctx, approvalPollInterval)
			}
		}
	}
}

// ── Activity wrappers ──────────────────────────────────────────────────────

func checkGate(ctx workflow.Context, name string, facts gates.Facts) (activity.GateCheckOutput, error) {
	var out activity.GateCheckOutput
	err := workflow.ExecuteActivity(ctx, GateCheckActivityName, activity.GateCheckInput{
		GateName: name,
		Facts:    facts,
	}).Get(ctx, &out)
	return out, err
}

func createApproval(ctx workflow.Context, in WaitGateInput) (activity.ApprovalCreateOutput, error) {
	var out activity.ApprovalCreateOutput
	input := activity.ApprovalCreateInput{
		WorkspaceID: in.WorkspaceID,
		GateName:    in.Name,
		RequestedBy: in.RequestedBy,
		RiskLevel:   in.RiskLevel,
		Timeout:     in.Timeout,
		WorkflowID:  in.WorkflowID,
		TaskRunID:   in.TaskRunID,
		StageRunID:  in.StageRunID,
		RunID:       in.RunID,
	}
	err := workflow.ExecuteActivity(ctx, ApprovalCreateActivityName, input).Get(ctx, &out)
	return out, err
}

func pollApproval(ctx workflow.Context, workspaceID uuid.UUID, approvalID string) (string, string, error) {
	var out activity.ApprovalPollOutput
	err := workflow.ExecuteActivity(ctx, ApprovalPollActivityName, activity.ApprovalPollInput{
		WorkspaceID: workspaceID,
		ApprovalID:  approvalID,
	}).Get(ctx, &out)
	return out.Status, out.Reason, err
}

func expireApproval(ctx workflow.Context, workspaceID uuid.UUID, approvalID string) error {
	return workflow.ExecuteActivity(ctx, ApprovalExpireActivityName, activity.ApprovalExpireInput{
		WorkspaceID: workspaceID,
		ApprovalID:  approvalID,
	}).Get(ctx, nil)
}

func decideApproval(ctx workflow.Context, workspaceID uuid.UUID, approvalID string, approved bool, decidedBy string, comment string) error {
	by, err := uuid.Parse(decidedBy)
	if err != nil {
		by = uuid.Nil
	}
	return workflow.ExecuteActivity(ctx, ApprovalDecideActivityName, activity.ApprovalDecideInput{
		WorkspaceID: workspaceID,
		ApprovalID:  approvalID,
		Approved:    approved,
		DecidedBy:   by,
		Comment:     comment,
	}).Get(ctx, nil)
}
