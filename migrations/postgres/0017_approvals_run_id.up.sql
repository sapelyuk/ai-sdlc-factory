-- Name: 0017_approvals_run_id
-- Description: persist the SDLC run reference on approval requests
-- References: SDLC_STATE_MACHINE.md §4 (Gates), NFR-9, FR-OPS-1, ADR-013, Issue #20

BEGIN;

-- run_id links a request to the run that opened it, so a decision can be
-- attributed to one run together with its cost and its artifacts (NFR-9).
-- The reference mirrors task_run_id and stage_run_id from migration 0002:
-- it is nullable because a request may be raised before a run exists, and
-- it cascades so that removing a run does not orphan its queue entries.
ALTER TABLE approvals ADD COLUMN run_id UUID NULL REFERENCES runs(id) ON DELETE CASCADE;

-- A run lists the requests it is still waiting on.
CREATE INDEX approvals_run_id_idx ON approvals (run_id)
    WHERE run_id IS NOT NULL;

COMMIT;
