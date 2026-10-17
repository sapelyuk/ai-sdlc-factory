-- Name: 0007_init_audit_and_cost
-- Description: audit_log, llm_calls
-- References: SYSTEM_DESIGN.md §10, ADR-013
-- FR-OPS, NFR-4, NFR-5

BEGIN;

-- ── audit_log ───────────────────────────────────────────────
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID NULL,
    details JSONB NOT NULL DEFAULT '{}',
    risk_level TEXT NOT NULL DEFAULT 'R0' CHECK (risk_level IN ('R0', 'R1', 'R2', 'R3')),
    result TEXT NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failed', 'blocked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_workspace_id_idx ON audit_log (workspace_id);
CREATE INDEX audit_log_action_idx ON audit_log (action);
CREATE INDEX audit_log_resource_idx ON audit_log (resource_type, resource_id);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id);

-- ── llm_calls ───────────────────────────────────────────────
CREATE TABLE llm_calls (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    task_run_id UUID NULL REFERENCES task_runs(id) ON DELETE SET NULL,
    agent_id UUID NULL REFERENCES agents(id) ON DELETE SET NULL,
    model_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    total_tokens INT NOT NULL DEFAULT 0,
    cost_usd NUMERIC(12,6) NOT NULL DEFAULT 0.0,
    duration_ms INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'success' CHECK (status IN ('success', 'failed', 'timeout', 'rate_limited')),
    error_message TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT prompt_tokens_positive CHECK (prompt_tokens >= 0),
    CONSTRAINT completion_tokens_positive CHECK (completion_tokens >= 0),
    CONSTRAINT total_tokens_positive CHECK (total_tokens >= 0),
    CONSTRAINT cost_usd_non_negative CHECK (cost_usd >= 0),
    CONSTRAINT duration_ms_positive CHECK (duration_ms >= 0)
);

CREATE INDEX llm_calls_workspace_id_idx ON llm_calls (workspace_id);
CREATE INDEX llm_calls_task_run_id_idx ON llm_calls (task_run_id);
CREATE INDEX llm_calls_agent_id_idx ON llm_calls (agent_id);
CREATE INDEX llm_calls_created_at_idx ON llm_calls (created_at);
CREATE INDEX llm_calls_model_idx ON llm_calls (model_name);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON audit_log
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE llm_calls ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON llm_calls
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
