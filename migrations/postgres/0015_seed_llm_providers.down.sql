-- Name: 0015_seed_llm_providers
-- Description: Remove the Gemini, DeepSeek and Groq model profile seeds and
--   the partial index introduced by this migration.
BEGIN;

DELETE FROM llm_model_profiles
WHERE provider IN ('google', 'deepseek', 'groq');

DROP INDEX IF EXISTS idx_llm_model_profiles_enabled;

COMMIT;
