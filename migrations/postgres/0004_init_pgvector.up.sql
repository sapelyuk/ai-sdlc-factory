-- Name: 0004_init_pgvector
-- Description: pgvector extension, kb_embeddings, HNSW index
-- References: SYSTEM_DESIGN.md §9.2, ADR-003
-- FR-KB, NFR-10

BEGIN;

-- ── pgvector extension ──────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS vector;

-- ── kb_embeddings ───────────────────────────────────────────
CREATE TABLE kb_embeddings (
    document_id UUID PRIMARY KEY REFERENCES kb_documents(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    embedding VECTOR(1536) NOT NULL,
    model_name TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── HNSW index for cosine similarity search ─────────────────
CREATE INDEX kb_embeddings_hnsw_idx ON kb_embeddings USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE kb_embeddings ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON kb_embeddings
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
