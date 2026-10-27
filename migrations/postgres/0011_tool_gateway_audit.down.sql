-- Name: 0011_tool_gateway_audit (down)
-- Description: Drop the Tool Gateway audit log.

BEGIN;

DROP TABLE IF EXISTS tool_call_audit_log;

COMMIT;
