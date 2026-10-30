-- Name: 0015_seed_llm_providers
-- Description: Remove the Gemini, DeepSeek and Groq model profile seeds and
--   the partial index introduced by this migration.
BEGIN;

DROP INDEX IF EXISTS idx_llm_model_profiles_enabled;

-- Remove only the rows inserted by this migration.
DELETE FROM llm_model_profiles
WHERE (profile_name, provider, model_id) IN (
    VALUES
      ('reasoning-strong', 'google',   'gemini-2.5-pro'),
      ('long-context',     'google',   'gemini-2.5-pro'),
      ('code-fast',        'google',   'gemini-2.5-flash'),
      ('cheap-bulk',       'google',   'gemini-2.5-flash'),
      ('reasoning-strong', 'deepseek', 'deepseek-v4-pro'),
      ('code-strong',      'deepseek', 'deepseek-v4-pro'),
      ('cheap-bulk',       'deepseek', 'deepseek-flash'),
      ('code-strong',      'groq',     'openai/gpt-oss-120b'),
      ('code-fast',        'groq',     'openai/gpt-oss-20b'),
      ('cheap-bulk',       'groq',     'openai/gpt-oss-20b')
);

COMMIT;
