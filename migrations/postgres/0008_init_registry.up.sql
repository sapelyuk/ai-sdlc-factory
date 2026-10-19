-- Name: 0008_init_registry
-- Description: registry tables for roles, teams, and processes with semver versioning
-- References: SYSTEM_DESIGN.md §4.3, §7.2–7.4, ADR-011
-- FR-TEAM, NFR-1

BEGIN;

-- ── roles ───────────────────────────────────────────────────
CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '1.0.0',
    category TEXT NOT NULL DEFAULT 'engineering' CHECK (category IN (
        'product', 'design', 'engineering', 'qa', 'devops', 'research', 'security'
    )),
    spec JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'deprecated')),
    author_id UUID NOT NULL,
    approver_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT role_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128),
    CONSTRAINT role_version_format CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    CONSTRAINT role_approver_is_not_author CHECK (approver_id IS NULL OR approver_id <> author_id)
);

CREATE UNIQUE INDEX roles_workspace_name_version_idx ON roles (workspace_id, name, version);
CREATE INDEX roles_workspace_id_idx ON roles (workspace_id);
CREATE INDEX roles_category_idx ON roles (category);
CREATE INDEX roles_status_idx ON roles (status);

-- ── teams ───────────────────────────────────────────────────
CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '1.0.0',
    spec JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'deprecated')),
    author_id UUID NOT NULL,
    approver_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT team_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128),
    CONSTRAINT team_version_format CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    CONSTRAINT team_approver_is_not_author CHECK (approver_id IS NULL OR approver_id <> author_id)
);

CREATE UNIQUE INDEX teams_workspace_name_version_idx ON teams (workspace_id, name, version);
CREATE INDEX teams_workspace_id_idx ON teams (workspace_id);
CREATE INDEX teams_status_idx ON teams (status);

-- ── processes ───────────────────────────────────────────────
CREATE TABLE processes (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '1.0.0',
    spec JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'deprecated')),
    author_id UUID NOT NULL,
    approver_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT process_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128),
    CONSTRAINT process_version_format CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    CONSTRAINT process_approver_is_not_author CHECK (approver_id IS NULL OR approver_id <> author_id)
);

CREATE UNIQUE INDEX processes_workspace_name_version_idx ON processes (workspace_id, name, version);
CREATE INDEX processes_workspace_id_idx ON processes (workspace_id);
CREATE INDEX processes_status_idx ON processes (status);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON roles
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE teams ENABLE ROW LEVEL SECURITY;
ALTER TABLE teams FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON teams
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE processes ENABLE ROW LEVEL SECURITY;
ALTER TABLE processes FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON processes
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

-- ── Trigger: update updated_at ──────────────────────────────
CREATE OR REPLACE FUNCTION registry_update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_roles_updated_at BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION registry_update_updated_at_column();

CREATE TRIGGER update_teams_updated_at BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION registry_update_updated_at_column();

CREATE TRIGGER update_processes_updated_at BEFORE UPDATE ON processes
    FOR EACH ROW EXECUTE FUNCTION registry_update_updated_at_column();

COMMIT;
