-- Name: 0004_init_pgvector
-- Description: Rollback kb_embeddings, drop pgvector extension
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS kb_embeddings CASCADE;
DROP EXTENSION IF EXISTS vector CASCADE;

COMMIT;
