// Package audit defines the event payload for worker audit activities and
// helper functions for building actor identifiers. It is a thin package that
// the workflow and activity layers depend on (issue #14).
package audit

import (
	"github.com/google/uuid"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// Event is the input of the audit activity: one append to the global
// tamper-evident audit log.
//
// Workflows cannot write to the database directly — Temporal requires
// deterministic code — so every audit-worthy step of a run is recorded by
// calling the activity with this struct.
type Event struct {
	// WorkspaceID is the tenant that owns the chain. It is mandatory: the row
	// is partitioned and RLS-filtered by it.
	WorkspaceID uuid.UUID `json:"workspace_id"`
	// ActorID is the prefixed actor ("agent:<uuid>", "system:worker", ...).
	ActorID string `json:"actor_id"`
	// Action is one of the sdkaudit.Action* constants.
	Action string `json:"action"`
	// IdempotencyKey is stable across workflow/activity retries.
	IdempotencyKey string `json:"idempotency_key"`
	// ResourceID identifies the affected resource (task run id, handoff owner).
	ResourceID string `json:"resource_id"`
	// Details is structured context. Secret-shaped values are redacted by the
	// shared auditor before hashing.
	Details map[string]any `json:"details,omitempty"`
}

// ActorForRun returns the actor identifier of a run: the agent that executes
// it when the caller supplied one, otherwise the worker itself acting as the
// platform system actor.
func ActorForRun(agentID string) string {
	if id, err := uuid.Parse(agentID); err == nil {
		return sdkaudit.AgentActor(id)
	}
	return sdkaudit.SystemActor("worker")
}
