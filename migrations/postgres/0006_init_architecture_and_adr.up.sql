-- Name: 0006_init_architecture_and_adr
-- Description: diagrams, architecture_models, adrs
-- References: SYSTEM_DESIGN.md §6, ADR-006, ADR-012
-- FR-ARCH, FR-ARCH-7

BEGIN;

-- ── diagrams ────────────────────────────────────────────────
CREATE TABLE diagrams (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id UUID NULL,
    name TEXT NOT NULL,
    diagram_type TEXT NOT NULL CHECK (diagram_type IN ('structurizr', 'mermaid', 'plantuml')),
    content TEXT NOT NULL,
    version INT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT diagram_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 256)
);

CREATE INDEX diagrams_workspace_id_idx ON diagrams (workspace_id);
CREATE INDEX diagrams_project_id_idx ON diagrams (project_id);
CREATE INDEX diagrams_status_idx ON diagrams (status);

-- ── architecture_models ─────────────────────────────────────
CREATE TABLE architecture_models (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id UUID NULL,
    name TEXT NOT NULL,
    model_version TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT 'structurizr' CHECK (format IN ('structurizr', 'mermaid', 'plantuml')),
    version INT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'deprecated')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT arch_model_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 256)
);

CREATE INDEX architecture_models_workspace_id_idx ON architecture_models (workspace_id);
CREATE INDEX architecture_models_project_id_idx ON architecture_models (project_id);
CREATE INDEX architecture_models_status_idx ON architecture_models (status);

-- ── adrs ────────────────────────────────────────────────────
CREATE TABLE adrs (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id UUID NULL,
    adr_number TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'accepted', 'superseded', 'deprecated', 'requires_review')),
    content TEXT NOT NULL,
    decision TEXT NULL,
    context TEXT NULL,
    consequences TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT adr_number_length CHECK (char_length(adr_number) >= 1 AND char_length(adr_number) <= 64),
    CONSTRAINT adr_title_length CHECK (char_length(title) >= 1 AND char_length(title) <= 512)
);

CREATE INDEX adrs_workspace_id_idx ON adrs (workspace_id);
CREATE INDEX adrs_project_id_idx ON adrs (project_id);
CREATE INDEX adrs_status_idx ON adrs (status);
CREATE UNIQUE INDEX adrs_workspace_number_idx ON adrs (workspace_id, adr_number);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE diagrams ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON diagrams
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE architecture_models ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON architecture_models
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE adrs ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON adrs
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
