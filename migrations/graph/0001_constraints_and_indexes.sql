-- Name: 0001_constraints_and_indexes
-- Description: Property graph constraints and additional indexes
-- References: ADR-003, ONTOLOGY_SPEC.md §6.1
-- Note: sem_nodes.id and sem_edges.id are already PRIMARY KEY (unique).
--        This file adds explicit UNIQUE constraints and temporal indexes.

BEGIN;

-- ── Explicit unique constraints (redundant with PK but self-documenting) ──
-- sem_nodes.id and sem_edges.id are PRIMARY KEY, so already unique.
-- Keeping this file for future graph-specific constraints.

-- ── Temporal index for sem_edges ────────────────────────────
-- Index for querying edges valid at a specific point in time
CREATE INDEX IF NOT EXISTS sem_edges_temporal_idx ON sem_edges (valid_from, valid_to)
    WHERE valid_to IS NULL OR valid_to > now();

-- ── Full-text search on sem_nodes label ─────────────────────
CREATE INDEX IF NOT EXISTS sem_nodes_label_fts_idx ON sem_nodes USING gin (
    to_tsvector('english', label || ' ' || coalesce(props->>'name', ''))
);

-- ── Composite index for subgraph traversal ──────────────────
-- Fetch all edges connected to a set of nodes
CREATE INDEX IF NOT EXISTS sem_edges_traversal_idx ON sem_edges (src_id, kind, dst_id);

COMMIT;
