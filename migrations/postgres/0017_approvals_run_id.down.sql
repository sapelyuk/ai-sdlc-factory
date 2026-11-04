-- Name: 0017_approvals_run_id
-- Description: reverse of migration 0017 (approvals run reference)
-- References: Issue #20

BEGIN;

-- Drop the index added by 0017, then the column it covers.
DROP INDEX IF EXISTS approvals_run_id_idx;
ALTER TABLE approvals DROP COLUMN IF EXISTS run_id;

COMMIT;
