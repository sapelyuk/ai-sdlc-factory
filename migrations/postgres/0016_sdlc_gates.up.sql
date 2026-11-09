-- Name: 0016_sdlc_gates
-- Description: extend approvals into a human_approval queue (deadline, context, workflow link)
-- References: SDLC_STATE_MACHINE.md §4 (Gates), §7 (Circuit breakers), ADR-013, FR-OPS-1, Issue #20

BEGIN;

-- deadline_at is the absolute answer-by timestamp of a human_approval request
-- (SDLC_STATE_MACHINE.md §7: deadline per StageRun, 24 hours by default). It is
-- NULL for rows created before this migration and for gate types that never
-- wait on a human.
ALTER TABLE approvals ADD COLUMN deadline_at TIMESTAMPTZ NULL;

-- context carries the transition payload the owner needs to decide: from/to
-- state, artifact links, task and run references (SYSTEM_DESIGN.md §8.4).
-- Secrets never enter this column: it is rendered in the owner UI.
ALTER TABLE approvals ADD COLUMN context JSONB NOT NULL DEFAULT '{}'::jsonb;

-- risk_level pins the autonomy level of the gated action (ADR-013). The gate
-- definition decides it, not the caller, so a request cannot downgrade its own
-- risk by asking a different actor to approve it.
ALTER TABLE approvals ADD COLUMN risk_level TEXT NOT NULL DEFAULT 'R3'
    CHECK (risk_level IN ('R0', 'R1', 'R2', 'R3'));

-- workflow_id links the request to the Temporal workflow that waits on it, so
-- a decision can be delivered as a signal instead of waiting for the next poll
-- (ADR-002: durable execution).
ALTER TABLE approvals ADD COLUMN workflow_id TEXT NOT NULL DEFAULT '';

-- decided_at records when the owner answered. updated_at also moves on
-- expiration, so the queue needs a separate column to report the real decision
-- time to the UI.
ALTER TABLE approvals ADD COLUMN decided_at TIMESTAMPTZ NULL;

-- An unanswered request must leave the queue: 'expired' is the terminal state
-- reached when deadline_at passes without a decision (SDLC_STATE_MACHINE.md
-- §7 — the action is NOT performed).
ALTER TABLE approvals DROP CONSTRAINT approvals_status_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'expired'));

-- The owner queue shows pending requests ordered by their deadline; the worker
-- polls a single request by id (covered by the primary key).
CREATE INDEX approvals_queue_idx ON approvals (workspace_id, deadline_at, created_at)
    WHERE status = 'pending';

-- A run must be able to find the requests it is waiting on.
CREATE INDEX approvals_workflow_id_idx ON approvals (workflow_id)
    WHERE workflow_id <> '';

COMMIT;
