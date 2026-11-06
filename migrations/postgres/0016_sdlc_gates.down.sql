-- Name: 0016_sdlc_gates
-- Description: reverse of migration 0016 (approvals gate extension)
-- References: Issue #20

BEGIN;

-- Reverse every change in reverse order.

-- Drop indexes first (must exist before DROP).
DROP INDEX IF EXISTS approvals_workflow_id_idx;
DROP INDEX IF EXISTS approvals_queue_idx;

-- Restore the original status check constraint (pre-0016: pending/approved/rejected).
-- 'expired' rows are folded into 'rejected' first: the pre-0016 constraint does
-- not know 'expired', and an unanswered request is a rejection in every sense
-- that matters to the old schema.
UPDATE approvals SET status = 'rejected' WHERE status = 'expired';
ALTER TABLE approvals DROP CONSTRAINT approvals_status_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_status_check
    CHECK (status IN ('pending', 'approved', 'rejected'));

-- Remove the five columns added by 0016.
ALTER TABLE approvals DROP COLUMN IF EXISTS decided_at;
ALTER TABLE approvals DROP COLUMN IF EXISTS workflow_id;
ALTER TABLE approvals DROP COLUMN IF EXISTS risk_level;
ALTER TABLE approvals DROP COLUMN IF EXISTS context;
ALTER TABLE approvals DROP COLUMN IF EXISTS deadline_at;

COMMIT;
