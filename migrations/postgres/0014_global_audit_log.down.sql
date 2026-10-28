-- Name: 0014_global_audit_log
-- Description: Rollback the global tamper-evident audit log transformation.
BEGIN;

DROP TABLE IF EXISTS audit_log;

COMMIT;
