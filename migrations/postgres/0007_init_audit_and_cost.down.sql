-- Name: 0007_init_audit_and_cost
-- Description: Rollback audit_log, llm_calls
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS llm_calls CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;

COMMIT;
