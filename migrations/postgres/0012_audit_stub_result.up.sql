-- Name: 0012_audit_stub_result
-- Description: Add 'stub' to the audit result CHECK constraint (issue #10 review F-1).
-- References: ADR-013 (R0-R3), NFR-5 (security)

BEGIN;

ALTER TABLE tool_call_audit_log
    DROP CONSTRAINT tool_call_audit_log_result_check;

ALTER TABLE tool_call_audit_log
    ADD CONSTRAINT tool_call_audit_log_result_check
    CHECK (result IN ('success', 'denied', 'error', 'stub'));

COMMIT;
