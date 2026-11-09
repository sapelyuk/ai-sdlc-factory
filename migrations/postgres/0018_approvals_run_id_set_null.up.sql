-- Name: 0018_approvals_run_id_set_null
-- Description: replace ON DELETE CASCADE with ON DELETE SET NULL for approvals.run_id
-- References: FR-OPS-1 (audit trail), ADR-013, Issue #20, PR #119

BEGIN;

-- The FK constraint was created inline in 0017 as
--   ADD COLUMN run_id UUID NULL REFERENCES runs(id) ON DELETE CASCADE
-- PostgreSQL auto-names it "approvals_run_id_fkey".
-- Remove CASCADE so that approval records survive run cleanup.
ALTER TABLE approvals
DROP CONSTRAINT IF EXISTS approvals_run_id_fkey;

ALTER TABLE approvals
ADD CONSTRAINT approvals_run_id_fkey
FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE SET NULL;

COMMIT;
