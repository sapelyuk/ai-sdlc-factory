-- Name: 0015_seed_llm_providers
-- Description: Remove the Gemini, DeepSeek and Groq model profile seeds.
BEGIN;

DELETE FROM llm_model_profiles
WHERE provider IN ('google', 'deepseek', 'groq');

COMMIT;
