-- Name: 0010_llm_gateway
-- Description: LLM Gateway tables — llm_model_profiles (global catalog),
--               model_health, llm_logical_calls, llm_attempts.
-- References: ADR-021 (routing, fallback per (provider, model), cost attribution),
--             SYSTEM_DESIGN.md §8.5, §11.4
-- Notes:      The table is named llm_model_profiles to avoid conflict with
--               the workspace-scoped model_profiles from migration 0001.
--               This is a global (non-workspace-scoped) catalog; model changes
--               are R2 actions per ADR-021.

BEGIN;

-- ── Model Catalog: profile → specific (pinned) model version ──
CREATE TABLE llm_model_profiles (
    profile_name      TEXT NOT NULL,       -- code-strong, reasoning-strong, ...
    provider          TEXT NOT NULL,       -- anthropic, openai, google, ollama
    model_id          TEXT NOT NULL,       -- PINNED version, e.g. claude-sonnet-4-20250101
    priority          INT NOT NULL DEFAULT 0,
    capabilities      JSONB NOT NULL,
    cost_per_mtok_in  NUMERIC(10,4),
    cost_per_mtok_out NUMERIC(10,4),
    enabled           BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_name, provider, model_id)
);

-- ── Health state per (provider, model) ────────────────────────
CREATE TABLE model_health (
    provider             TEXT NOT NULL,
    model_id             TEXT NOT NULL,
    consecutive_failures INT NOT NULL DEFAULT 0,
    cooldown_until       TIMESTAMPTZ,
    last_error_at        TIMESTAMPTZ,
    last_error_type      TEXT,
    last_success_at      TIMESTAMPTZ,
    PRIMARY KEY (provider, model_id)
);

-- ── Logical calls (attribution layer) ─────────────────────────
CREATE TABLE llm_logical_calls (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id     UUID NOT NULL,
    run_id           UUID,
    task_id          UUID,
    agent_id         UUID,
    profile_name     TEXT NOT NULL,
    model_id_used    TEXT NOT NULL,
    provider_used    TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'partial')),
    attempt_count    INT NOT NULL DEFAULT 0,
    final_tokens_in  INT,
    final_tokens_out INT,
    final_cost_usd   NUMERIC(10,4),
    total_tokens_in  INT NOT NULL DEFAULT 0,
    total_tokens_out INT NOT NULL DEFAULT 0,
    total_cost_usd   NUMERIC(10,4) NOT NULL DEFAULT 0,
    started_at       TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ
);

CREATE INDEX idx_logical_calls_run ON llm_logical_calls (run_id);
CREATE INDEX idx_logical_calls_workspace ON llm_logical_calls (workspace_id, started_at);

ALTER TABLE llm_logical_calls ENABLE ROW LEVEL SECURITY;

CREATE POLICY llm_logical_calls_ws ON llm_logical_calls
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

-- ── Attempts (spend layer) ────────────────────────────────────
CREATE TABLE llm_attempts (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    logical_call_id  UUID NOT NULL REFERENCES llm_logical_calls(id) ON DELETE CASCADE,
    attempt_num      INT NOT NULL,
    provider         TEXT NOT NULL,
    model_id         TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN (
        'succeeded', 'failed_404', 'failed_429', 'failed_5xx',
        'failed_auth', 'failed_bad_request', 'failed_timeout', 'failed_other'
    )),
    error_message    TEXT,
    tokens_in        INT,
    tokens_out       INT,
    cost_usd         NUMERIC(10,4),
    latency_ms       INT,
    started_at       TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ
);

CREATE INDEX idx_attempts_logical ON llm_attempts (logical_call_id);

COMMIT;

-- ── Seed data ─────────────────────────────────────────────────
INSERT INTO llm_model_profiles (profile_name, provider, model_id, priority, capabilities, cost_per_mtok_in, cost_per_mtok_out) VALUES
    ('code-strong',      'anthropic',  'claude-sonnet-4-20250101',      0, '{"context_window":200000,"supports_tools":true}', 3.00, 15.00),
    ('code-strong',      'openai',     'gpt-4o-2024-11-20',            1, '{"context_window":128000,"supports_tools":true}', 2.50, 10.00),
    ('reasoning-strong', 'anthropic',  'claude-opus-4-20250101',       0, '{"context_window":200000,"supports_tools":true}', 15.00, 75.00),
    ('reasoning-strong', 'openai',     'o1-2024-12-17',                1, '{"context_window":200000,"supports_tools":true}', 15.00, 60.00),
    ('code-fast',        'openai',     'gpt-4o-mini-2024-07-18',       0, '{"context_window":128000,"supports_tools":true}', 0.15, 0.60),
    ('cheap-bulk',       'openai',     'gpt-4o-mini-2024-07-18',       0, '{"context_window":128000,"supports_tools":true}', 0.15, 0.60),
    ('local-private',    'ollama',     'llama3.1:8b-instruct-q4_K_M',  0, '{"context_window":128000,"supports_tools":false}', 0.00, 0.00);
