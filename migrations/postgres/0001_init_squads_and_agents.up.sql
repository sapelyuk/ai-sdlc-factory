-- Name: 0001_init_squads_and_agents
-- Description: workspaces, squads, agents, model_profiles
-- References: SYSTEM_DESIGN.md §7.1, ADR-003, ONTOLOGY_SPEC.md §3.1
-- FR-TEAM, NFR-1, NFR-5

BEGIN;

-- ── workspaces ──────────────────────────────────────────────
CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128)
);

CREATE UNIQUE INDEX workspaces_name_owner_idx ON workspaces (name, owner_id);

-- ── squads ──────────────────────────────────────────────────
CREATE TABLE squads (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT squad_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128)
);

CREATE UNIQUE INDEX squads_workspace_name_idx ON squads (workspace_id, name);

-- ── model_profiles ──────────────────────────────────────────
CREATE TABLE model_profiles (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    model_name TEXT NOT NULL,
    capability TEXT NOT NULL CHECK (capability IN ('code-strong', 'code-fast', 'reasoning-strong', 'reasoning-fast', 'long-context', 'vision-capable', 'cheap-bulk', 'local-private')),
    max_tokens INT NOT NULL DEFAULT 8192,
    temperature NUMERIC(3,2) NOT NULL DEFAULT 0.7,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT model_profile_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128),
    CONSTRAINT model_name_length CHECK (char_length(model_name) >= 1 AND char_length(model_name) <= 256),
    CONSTRAINT temperature_range CHECK (temperature >= 0 AND temperature <= 2)
);

CREATE UNIQUE INDEX model_profiles_workspace_name_idx ON model_profiles (workspace_id, name);

-- ── agents ──────────────────────────────────────────────────
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    squad_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    role_version TEXT NOT NULL,
    model_profile_id UUID NOT NULL REFERENCES model_profiles(id) ON DELETE RESTRICT,
    autonomy_level TEXT NOT NULL DEFAULT 'R0' CHECK (autonomy_level IN ('R0', 'R1', 'R2', 'R3')),
    max_steps INT NOT NULL DEFAULT 60,
    max_cost_usd NUMERIC(10,4) NOT NULL DEFAULT 3.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 128),
    CONSTRAINT role_version_length CHECK (char_length(role_version) >= 1 AND char_length(role_version) <= 64)
);

CREATE UNIQUE INDEX agents_squad_name_idx ON agents (squad_id, name);
CREATE INDEX agents_workspace_id_idx ON agents (workspace_id);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON workspaces
    USING (id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE squads ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON squads
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE model_profiles ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON model_profiles
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON agents
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
