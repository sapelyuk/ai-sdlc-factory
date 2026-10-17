-- Name: 0005_init_semantic_graph
-- Description: sem_nodes, sem_edges, sem_embeddings — property graph
-- References: SYSTEM_DESIGN.md §5.3, ADR-003, ONTOLOGY_SPEC.md §6.1
-- FR-SEM-1, FR-SEM-6, NFR-10

BEGIN;

-- ── sem_nodes ───────────────────────────────────────────────
CREATE TABLE sem_nodes (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id UUID NULL,
    layer TEXT NOT NULL CHECK (layer IN ('SLP', 'SLPr', 'AM')),
    kind TEXT NOT NULL,
    label TEXT NOT NULL,
    props JSONB NOT NULL DEFAULT '{}',
    version INT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'deprecated', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- GIN index for JSONB props queries
CREATE INDEX sem_nodes_props_gin_idx ON sem_nodes USING GIN (props);

-- Btree indexes for filtering
CREATE INDEX sem_nodes_kind_idx ON sem_nodes (kind);
CREATE INDEX sem_nodes_workspace_id_idx ON sem_nodes (workspace_id);
CREATE INDEX sem_nodes_project_id_idx ON sem_nodes (project_id) WHERE project_id IS NOT NULL;
CREATE INDEX sem_nodes_layer_kind_idx ON sem_nodes (layer, kind);
CREATE INDEX sem_nodes_status_idx ON sem_nodes (status);

-- ── sem_edges ───────────────────────────────────────────────
CREATE TABLE sem_edges (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    src_id UUID NOT NULL REFERENCES sem_nodes(id) ON DELETE CASCADE,
    dst_id UUID NOT NULL REFERENCES sem_nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    props JSONB NOT NULL DEFAULT '{}',
    valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_to TIMESTAMPTZ NULL
);

-- GIN index for edge props
CREATE INDEX sem_edges_props_gin_idx ON sem_edges USING GIN (props);

-- Btree indexes for graph traversal
CREATE INDEX sem_edges_workspace_id_idx ON sem_edges (workspace_id);
CREATE INDEX sem_edges_src_id_idx ON sem_edges (src_id);
CREATE INDEX sem_edges_dst_id_idx ON sem_edges (dst_id);
CREATE INDEX sem_edges_kind_idx ON sem_edges (kind);
CREATE INDEX sem_edges_valid_from_idx ON sem_edges (valid_from);

-- Composite index for temporal queries
CREATE INDEX sem_edges_src_dst_kind_idx ON sem_edges (src_id, dst_id, kind);

-- ── sem_embeddings ──────────────────────────────────────────
CREATE TABLE sem_embeddings (
    node_id UUID PRIMARY KEY REFERENCES sem_nodes(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    embedding VECTOR(1536) NOT NULL,
    model_name TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- HNSW index for cosine similarity search
CREATE INDEX sem_embeddings_hnsw_idx ON sem_embeddings USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- ── Row Level Security ──────────────────────────────────────
ALTER TABLE sem_nodes ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON sem_nodes
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE sem_edges ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON sem_edges
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

ALTER TABLE sem_embeddings ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation_policy ON sem_embeddings
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
