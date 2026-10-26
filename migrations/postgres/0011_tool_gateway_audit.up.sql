-- Name: 0011_tool_gateway_audit
-- Description: Hash-chained audit log for Tool Gateway calls (issue #10).
-- References: ADR-013 (R0-R3), ADR-022 (mcp-go), NFR-5 (security)
--
-- The generic audit_log table already exists in migration 0007. The Tool
-- Gateway needs a different shape (tool_name, agent_id, hash_chain) and a
-- result CHECK that allows 'denied' and 'error', so it gets its own table
-- instead of rewriting a released migration.

BEGIN;

CREATE TABLE tool_call_audit_log (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    tool_name TEXT NOT NULL,
    risk_level TEXT NOT NULL CHECK (risk_level IN ('R0', 'R1', 'R2', 'R3')),
    result TEXT NOT NULL CHECK (result IN ('success', 'denied', 'error')),
    error_message TEXT,
    hash_chain TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tool_call_audit_log_workspace_idx
    ON tool_call_audit_log (workspace_id, created_at);
CREATE INDEX tool_call_audit_log_tool_idx
    ON tool_call_audit_log (tool_name);
CREATE INDEX tool_call_audit_log_agent_idx
    ON tool_call_audit_log (agent_id);
CREATE UNIQUE INDEX tool_call_audit_log_hash_chain_idx
    ON tool_call_audit_log (hash_chain);

-- Each workspace has its own linear hash chain, so RLS also protects the
-- chain head read that computes hash_n.
ALTER TABLE tool_call_audit_log ENABLE ROW LEVEL SECURITY;

CREATE POLICY tool_call_audit_log_workspace_isolation ON tool_call_audit_log
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
