-- Name: 0005_init_semantic_graph
-- Description: Rollback sem_nodes, sem_edges, sem_embeddings
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS sem_embeddings CASCADE;
DROP TABLE IF EXISTS sem_edges CASCADE;
DROP TABLE IF EXISTS sem_nodes CASCADE;

COMMIT;
