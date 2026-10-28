package routing

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// auditWriteTimeout bounds the append of one audit event. The write is detached
// from the request context, so it needs a budget of its own.
const auditWriteTimeout = 5 * time.Second

// WithAuditor installs the auditor that records one `llm_call` event per
// finished logical call (issue #14). Passing nil restores the no-op auditor,
// so a deployment without an audit database keeps working unchanged.
func (r *Router) WithAuditor(a sdkaudit.Auditor) *Router {
	if a == nil {
		a = sdkaudit.Nop()
	}
	r.auditor = a
	return r
}

// auditLLMCall appends the global audit event of a finalized logical call.
//
// It is called exactly once per logical call, right after the row has been
// persisted in its final state, so the audit log and llm_logical_calls cannot
// diverge. A failed audit write is logged but never replaces the caller's
// result: losing an attribution event must not turn a successful generation
// into an error for the agent.
func (r *Router) auditLLMCall(ctx context.Context, call *domain.LogicalCall, cause error) {
	if call == nil || call.ID == uuid.Nil || r.auditor == nil {
		return
	}

	actor := sdkaudit.SystemActor("gateway-llm")
	if call.AgentID != nil {
		actor = sdkaudit.AgentActor(*call.AgentID)
	}

	ev := sdkaudit.Event{
		WorkspaceID: call.WorkspaceID,
		ActorID:     actor,
		Action:      sdkaudit.ActionLLMCall,
		ResourceID:  call.ID.String(),
		Details:     llmCallDetails(call, cause),
	}

	// The event is written on a context detached from the caller: the call
	// happened and consumed budget even if the client disconnected, so a
	// canceled request must not remove it from the audit log. This mirrors the
	// attribution repository, which finalizes the row the same way (NFR-3).
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()

	if err := r.auditor.Log(auditCtx, ev); err != nil {
		log.Printf("ERROR: audit llm_call %s: %v", call.ID, err)
	}
}

// llmCallDetails renders the logical call summary. Cost and token figures are
// the two-level attribution of ADR-021: final_* produced the result, total_*
// is the real spend including failed attempts.
func llmCallDetails(call *domain.LogicalCall, cause error) map[string]any {
	details := map[string]any{
		"logical_call_id":  call.ID.String(),
		"profile":          call.ProfileName,
		"provider":         call.ProviderUsed,
		"model":            call.ModelIDUsed,
		"status":           call.Status,
		"attempt_count":    call.AttemptCount,
		"total_tokens_in":  call.TotalTokensIn,
		"total_tokens_out": call.TotalTokensOut,
		"total_cost_usd":   call.TotalCostUSD,
	}
	if call.RunID != nil {
		details["run_id"] = call.RunID.String()
	}
	if call.TaskID != nil {
		details["task_id"] = call.TaskID.String()
	}
	if call.AgentID != nil {
		details["agent_id"] = call.AgentID.String()
	}
	if call.FinalTokensIn != nil {
		details["final_tokens_in"] = *call.FinalTokensIn
	}
	if call.FinalTokensOut != nil {
		details["final_tokens_out"] = *call.FinalTokensOut
	}
	if call.FinalCostUSD != nil {
		details["final_cost_usd"] = *call.FinalCostUSD
	}
	if cause != nil {
		// Provider error strings can echo request headers; the shared auditor
		// redacts known credential formats before hashing and storing.
		details["error"] = cause.Error()
	}
	return details
}
