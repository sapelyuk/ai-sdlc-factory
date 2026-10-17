-- Name: 0002_init_tasks_and_sdlc
-- Description: runs, stage_runs, task_runs, approvals
-- References: SYSTEM_DESIGN.md §7.1, SDLC_STATE_MACHINE.md
-- FR-TEAM, NFR-2

BEGIN;

-- ── runs ────────────────────────────────────────────────────
CREATE TABLE runs (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    process_version TEXT NOT NULL DEFAULT '',
    team_version TEXT NOT NULL DEFAULT '',
    slp_version TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'idea' CHECK (status IN (
        'idea', 'research', 'requirements', 'requirements_review',
        'design', 'architecture', 'architecture_review', 'planning',
        'in_dev', 'code_review', 'qa', 'build', 'release', 'deploy', 'monitoring'
    )),
    current_stage TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT run_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 256)
);

CREATE INDEX runs_workspace_id_idx ON runs (workspace_id);
CREATE INDEX runs_status_idx ON runs (status);

-- ── stage_runs ──────────────────────────────────────────────
CREATE TABLE stage_runs (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    stage_order INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'approved', 'rejected', 'skipped')),
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT stage_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128)
);

CREATE INDEX stage_runs_run_id_idx ON stage_runs (run_id);
CREATE INDEX stage_runs_workspace_id_idx ON stage_runs (workspace_id);

-- ── task_runs ───────────────────────────────────────────────
CREATE TABLE task_runs (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    stage_run_id UUID NOT NULL REFERENCES stage_runs(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    branch_name TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    step_count INT NOT NULL DEFAULT 0,
    cost_usd NUMERIC(10,4) NOT NULL DEFAULT 0.0,
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_run_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 256),
    CONSTRAINT step_count_positive CHECK (step_count >= 0),
    CONSTRAINT cost_usd_non_negative CHECK (cost_usd >= 0)
);

CREATE INDEX task_runs_stage_run_id_idx ON task_runs (stage_run_id);
CREATE INDEX task_runs_workspace_id_idx ON task_runs (workspace_id);
CREATE INDEX task_runs_agent_id_idx ON task_runs (agent_id);

-- ── approvals ───────────────────────────────────────────────
CREATE TABLE approvals (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    task_run_id UUID NULL REFERENCES task_runs(id) ON DELETE CASCADE,
    stage_run_id UUID NULL REFERENCES stage_runs(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    gate_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    requested_by UUID NOT NULL,
    approved_by UUID NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT approval_gate_name_length CHECK (char_length(gate_name) >= 1 AND char_length(gate_name) <= 128),
    CONSTRAINT approval_comment_length CHECK (char_length(comment) <= 4096)
);

CREATE INDEX approvals_task_run_id_idx ON approvals (task_run_id);
CREATE INDEX approvals_stage_run_id_idx ON approvals (stage_run_id);
CREATE INDEX approvals_workspace_id_idx ON approvals (workspace_id);
CREATE INDEX approvals_status_idx ON approvals (status);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON runs
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE stage_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON stage_runs
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE task_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON task_runs
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE approvals ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON approvals
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
