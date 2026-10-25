-- Name: 0010_llm_gateway (rollback)
-- Description: Drop LLM Gateway tables in reverse dependency order.

BEGIN;

DROP TABLE IF EXISTS llm_attempts;
DROP TABLE IF EXISTS llm_logical_calls;
DROP TABLE IF EXISTS model_health;
DROP TABLE IF EXISTS llm_model_profiles;

COMMIT;
