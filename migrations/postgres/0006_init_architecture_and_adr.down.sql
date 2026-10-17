-- Name: 0006_init_architecture_and_adr
-- Description: Rollback diagrams, architecture_models, adrs
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS adrs CASCADE;
DROP TABLE IF EXISTS architecture_models CASCADE;
DROP TABLE IF EXISTS diagrams CASCADE;

COMMIT;
