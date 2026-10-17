-- Name: 0003_init_knowledge_base
-- Description: kb_documents, kb_sources
-- References: SYSTEM_DESIGN.md §9, ADR-007
-- FR-KB, NFR-1

BEGIN;

-- ── kb_sources ──────────────────────────────────────────────
CREATE TABLE kb_sources (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('file', 'url', 'git')),
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT kb_source_name_length CHECK (char_length(name) >= 1 AND char_length(name) <= 256)
);

CREATE INDEX kb_sources_workspace_id_idx ON kb_sources (workspace_id);

-- ── kb_documents ────────────────────────────────────────────
CREATE TABLE kb_documents (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_id UUID NULL REFERENCES kb_sources(id) ON DELETE SET NULL,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    chunk_index INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'indexed' CHECK (status IN ('pending', 'indexed', 'failed', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT kb_doc_title_length CHECK (char_length(title) <= 1024),
    CONSTRAINT chunk_index_positive CHECK (chunk_index >= 0)
);

CREATE INDEX kb_documents_workspace_id_idx ON kb_documents (workspace_id);
CREATE INDEX kb_documents_source_id_idx ON kb_documents (source_id);
CREATE INDEX kb_documents_status_idx ON kb_documents (status);
CREATE INDEX kb_documents_workspace_status_idx ON kb_documents (workspace_id, status);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE kb_sources ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON kb_sources
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE kb_documents ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON kb_documents
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
