-- Name: 0003_init_knowledge_base
-- Description: Rollback kb_documents, kb_sources
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS kb_documents CASCADE;
DROP TABLE IF EXISTS kb_sources CASCADE;

COMMIT;
