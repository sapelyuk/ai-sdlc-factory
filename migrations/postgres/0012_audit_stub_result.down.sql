-- Name: 0012_audit_stub_result (down)
-- Description: Revert 'stub' from the audit result CHECK constraint.

BEGIN;

ALTER TABLE tool_call_audit_log
    DROP CONSTRAINT tool_call_audit_log_result_check;

ALTER TABLE tool_call_audit_log
    ADD CONSTRAINT tool_call_audit_log_result_check
    CHECK (result IN ('success', 'denied', 'error'));

COMMIT;
