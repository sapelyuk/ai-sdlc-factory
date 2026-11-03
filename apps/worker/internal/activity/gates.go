// Package activity registers Temporal activities for SDLC quality gates
// (issue #20, SDLC_STATE_MACHINE.md section 4).
package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
)

// ── GateCheckActivity ─────────────────────────────────────────────

// GateCheckInput is the input for the gate-check activity.
type GateCheckInput struct {
	// WorkspaceID scopes the audit event.
	WorkspaceID uuid.UUID `json:"workspace_id"`
	// GateName is the gate identifier from the registry.
	GateName string `json:"gate_name"`
	// Facts are the evidence the gate is evaluated against.
	Facts gates.Facts `json:"facts,omitempty"`
	// ApprovalTimeoutSeconds overrides the default human_approval deadline
	// (0 = use the gate default). Only meaningful for human_approval gates.
	ApprovalTimeoutSeconds int `json:"approval_timeout_seconds,omitempty"`
}

// GateCheckOutput is the result of a gate check.
type GateCheckOutput struct {
	Approved bool   `json:"approved"`
	Rejected bool   `json:"rejected"`
	Reason   string `json:"reason,omitempty"`
	Timeout  bool   `json:"timeout"`
	// Pending is true when the gate is neither passed nor blocked: the caller
	// must open or wait for an approval request.
	Pending bool `json:"pending"`
	// GateType is the registry type of the checked gate, so the workflow can
	// decide whether an approval request is the way to resolve a pending
	// result.
	GateType string `json:"gate_type"`
}

// GateCheckActivity evaluates a gate against the provided facts.
type GateCheckActivity struct {
	svc *gates.Service
}

// NewGateCheckActivity creates the activity with the given gate service.
func NewGateCheckActivity(svc *gates.Service) *GateCheckActivity {
	return &GateCheckActivity{svc: svc}
}

// Execute runs the gate check. Auto and agent_review gates are pure: they
// return a decision or a blocking reason. A human_approval gate without an
// approval ID in the facts cannot be evaluated yet, so it reports Pending:
// the caller must create a request first.
func (a *GateCheckActivity) Execute(ctx context.Context, input GateCheckInput) (GateCheckOutput, error) {
	gate, err := a.svc.Registry().Lookup(input.GateName)
	if err != nil {
		return GateCheckOutput{}, err
	}
	out := GateCheckOutput{GateType: string(gate.Type)}

	// No request has been opened for this human gate yet: nothing to evaluate.
	if gate.Type == gates.GateTypeHumanApproval &&
		input.Facts.GetString(gates.EvidenceApprovalID) == "" {
		out.Pending = true
		return out, nil
	}

	result, err := a.svc.Evaluate(ctx, input.GateName, input.Facts)
	if err != nil {
		return GateCheckOutput{}, err
	}
	out.Approved = result.Approved
	out.Rejected = result.Rejected
	out.Timeout = result.TimeoutExpired
	out.Reason = result.Reason
	out.Pending = result.IsPending()
	return out, nil
}

// ── ApprovalCreateActivity ───────────────────────────────────────

// ApprovalCreateInput is the input for the approval-create activity.
type ApprovalCreateInput struct {
	WorkspaceID uuid.UUID      `json:"workspace_id"`
	TaskRunID   *uuid.UUID     `json:"task_run_id,omitempty"`
	StageRunID  *uuid.UUID     `json:"stage_run_id,omitempty"`
	RunID       *uuid.UUID     `json:"run_id,omitempty"`
	GateName    string         `json:"gate_name"`
	RequestedBy uuid.UUID      `json:"requested_by"`
	RiskLevel   string         `json:"risk_level"`
	Timeout     time.Duration  `json:"timeout"`
	Context     map[string]any `json:"context,omitempty"`
	WorkflowID  string         `json:"workflow_id"`
}

// ApprovalCreateOutput is the result of creating an approval request.
type ApprovalCreateOutput struct {
	ApprovalID string `json:"approval_id"`
	DeadlineAt string `json:"deadline_at"`
}

// ApprovalCreateActivity creates a human_approval request in the DB.
type ApprovalCreateActivity struct {
	store gates.ApprovalStore
}

// NewApprovalCreateActivity creates the activity.
func NewApprovalCreateActivity(store gates.ApprovalStore) *ApprovalCreateActivity {
	return &ApprovalCreateActivity{store: store}
}

// Execute creates a pending request and persists it.
func (a *ApprovalCreateActivity) Execute(ctx context.Context, input ApprovalCreateInput) (ApprovalCreateOutput, error) {
	req := gates.BuildApprovalRequest(
		input.WorkspaceID,
		input.GateName,
		input.RequestedBy,
		input.Timeout,
		input.Context,
	)
	req.TaskRunID = input.TaskRunID
	req.StageRunID = input.StageRunID
	req.RunID = input.RunID
	req.RiskLevel = input.RiskLevel
	req.WorkflowID = input.WorkflowID

	if err := a.store.Create(ctx, req); err != nil {
		return ApprovalCreateOutput{}, err
	}
	return ApprovalCreateOutput{
		ApprovalID: req.ID.String(),
		DeadlineAt: req.DeadlineAt.Format(time.RFC3339),
	}, nil
}

// ── ApprovalPollActivity ─────────────────────────────────────────

// ApprovalPollInput is the input for the poll activity.
type ApprovalPollInput struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	ApprovalID  string    `json:"approval_id"`
}

// ApprovalPollOutput is the result of a poll.
type ApprovalPollOutput struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ApprovalPollActivity checks the current status of an approval request.
type ApprovalPollActivity struct {
	store gates.ApprovalStore
}

// NewApprovalPollActivity creates the activity.
func NewApprovalPollActivity(store gates.ApprovalStore) *ApprovalPollActivity {
	return &ApprovalPollActivity{store: store}
}

// Execute returns the current status of the approval request.
// Returns:
//
//	{Status: "approved"} → transition allowed;
//	{Status: "rejected"} → transition blocked;
//	{Status: "expired"}  → transition blocked;
//	{Status: "pending"}  → keep waiting.
//
// A database error is retryable.
func (a *ApprovalPollActivity) Execute(ctx context.Context, input ApprovalPollInput) (ApprovalPollOutput, error) {
	id, err := uuid.Parse(input.ApprovalID)
	if err != nil {
		return ApprovalPollOutput{}, temporal.NewNonRetryableApplicationError(
			"invalid approval ID", "InvalidInput", err)
	}
	req, err := a.store.Get(ctx, id)
	if err != nil {
		return ApprovalPollOutput{}, fmt.Errorf("poll approval %s: %w", id, err)
	}
	out := ApprovalPollOutput{Status: string(req.Status)}
	if req.Status != gates.ApprovalPending {
		out.Reason = req.Comment
	}
	return out, nil
}

// ── ApprovalExpireActivity ───────────────────────────────────────

// ApprovalExpireInput is the input for the expire activity.
type ApprovalExpireInput struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	ApprovalID  string    `json:"approval_id"`
}

// ApprovalExpireActivity marks a pending request as expired when the workflow
// timer fires and no decision arrived in time.
type ApprovalExpireActivity struct {
	store gates.ApprovalStore
}

// NewApprovalExpireActivity creates the activity.
func NewApprovalExpireActivity(store gates.ApprovalStore) *ApprovalExpireActivity {
	return &ApprovalExpireActivity{store: store}
}

// Execute marks the request expired (idempotent: already-expired or decided
// requests are left untouched and return nil).
func (a *ApprovalExpireActivity) Execute(ctx context.Context, input ApprovalExpireInput) error {
	id, err := uuid.Parse(input.ApprovalID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(
			"invalid approval ID", "InvalidInput", err)
	}
	return a.store.Expire(ctx, id)
}

// ── ApprovalDecideActivity ─────────────────────────────────────────

// ApprovalDecideInput is the input for the decide activity.
type ApprovalDecideInput struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	ApprovalID  string    `json:"approval_id"`
	Approved    bool      `json:"approved"`
	DecidedBy   uuid.UUID `json:"decided_by"`
	Comment     string    `json:"comment,omitempty"`
}

// ApprovalDecideActivity records a human decision in the store.
type ApprovalDecideActivity struct {
	store gates.ApprovalStore
}

// NewApprovalDecideActivity creates the activity.
func NewApprovalDecideActivity(store gates.ApprovalStore) *ApprovalDecideActivity {
	return &ApprovalDecideActivity{store: store}
}

// Execute marks the approval as approved or rejected. It is idempotent:
// a request that is not pending (expired or already decided) returns nil
// without error, so the workflow can safely call it even if the decision
// arrived through another channel.
func (a *ApprovalDecideActivity) Execute(ctx context.Context, input ApprovalDecideInput) error {
	id, err := uuid.Parse(input.ApprovalID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(
			"invalid approval ID", "InvalidInput", err)
	}
	// ErrNotPending is treated as success: the decision is already recorded
	// (by the API or a previous signal), so the workflow can proceed.
	err = a.store.Decide(ctx, id, input.Approved, input.DecidedBy, input.Comment)
	if errors.Is(err, gates.ErrNotPending) {
		return nil
	}
	return err
}
