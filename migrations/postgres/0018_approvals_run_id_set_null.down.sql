-- Name: 0018_approvals_run_id_set_null
-- Description: reverse of migration 0018 — restore ON DELETE CASCADE
-- References: Issue #20, PR #119

BEGIN;

ALTER TABLE approvals
DROP CONSTRAINT IF EXISTS approvals_run_id_fkey;

ALTER TABLE approvals
ADD CONSTRAINT approvals_run_id_fkey
FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE;

COMMIT;
