-- Name: 0015_seed_llm_providers
-- Description: Seed llm_model_profiles for three new OpenAI-compatible
--   providers: Gemini (google), DeepSeek, Groq (issue #112).
-- References: ADR-004 (capability profiles), ADR-008 (LLM Gateway in Go),
--   ADR-021 (pinned versions, priority ordering, cost attribution), NFR-9
-- Notes:      Every row is seeded with enabled = false. The owner activates a
--   provider after its API key is provisioned:
--     UPDATE llm_model_profiles SET enabled = true WHERE provider = 'google';
--   Model IDs, context windows and prices were verified against official
--   provider documentation on 2026-10-29:
--     https://ai.google.dev/gemini-api/docs/models
--     https://ai.google.dev/gemini-api/docs/pricing
--     https://api-docs.deepseek.com/quick_start/pricing
--     https://console.groq.com/docs/models
--   Pinning (ADR-021):
--     * Google stable names (gemini-2.5-pro, gemini-2.5-flash) are version
--       identifiers that do not hot-swap; only "-latest" aliases do, and
--       catalog.Load rejects those.
--     * DeepSeek and Groq expose only floating aliases without a pinned
--       timestamp variant. The provider-documented alias is used as the
--       stable identifier (Known Limitation, issue #112).
--   Prices are per 1M tokens, Standard tier:
--     * Gemini 2.5 Pro prices apply to prompts <= 200k tokens.
--     * DeepSeek prices are the off-peak rate; peak hours cost 2x (see
--       Known Limitations in apps/gateway-llm/README.md).
--   Priorities never displace the existing 0010 seeds
--   (anthropic = 0, openai = 1 per profile); new providers start at 2.

BEGIN;

-- ── Catalog read path ────────────────────────────────────────
-- catalog.Load reads WHERE enabled = true ORDER BY profile_name, priority.
-- The primary key covers the ordering but not the enabled filter, so this
-- partial index backs the query once providers are activated.
CREATE INDEX IF NOT EXISTS idx_llm_model_profiles_enabled
    ON llm_model_profiles (profile_name, priority)
    WHERE enabled = true;

INSERT INTO llm_model_profiles
    (profile_name, provider, model_id, priority, capabilities, cost_per_mtok_in, cost_per_mtok_out, enabled)
VALUES
    -- ── Gemini (Google, OpenAI-compatible layer) ──
    ('reasoning-strong', 'google',   'gemini-2.5-pro',            2, '{"context_window":1048576,"supports_tools":true,"supports_vision":true}', 1.25,  10.00, false),
    ('long-context',     'google',   'gemini-2.5-pro',            2, '{"context_window":1048576,"supports_tools":true,"supports_vision":true}', 1.25,  10.00, false),
    ('code-fast',        'google',   'gemini-2.5-flash',          2, '{"context_window":1048576,"supports_tools":true,"supports_vision":true}', 0.30,   2.50, false),
    ('cheap-bulk',       'google',   'gemini-2.5-flash',          2, '{"context_window":1048576,"supports_tools":true,"supports_vision":true}', 0.30,   2.50, false),
    -- ── DeepSeek (OpenAI-compatible; alias only, no pinned version) ──
    ('reasoning-strong', 'deepseek', 'deepseek-v4-pro',           3, '{"context_window":1000000,"supports_tools":true}',                        0.66,   1.98, false),
    ('code-strong',      'deepseek', 'deepseek-v4-pro',           3, '{"context_window":1000000,"supports_tools":true}',                        0.66,   1.98, false),
    ('cheap-bulk',       'deepseek', 'deepseek-flash',            2, '{"context_window":1000000,"supports_tools":true,"supports_vision":true}', 0.15,   0.60, false),
    -- ── Groq (OpenAI-compatible; alias only, no pinned version) ──
    ('code-strong',      'groq',     'openai/gpt-oss-120b',       2, '{"context_window":131072,"supports_tools":true}',                         0.15,   0.60, false),
    ('code-fast',        'groq',     'openai/gpt-oss-20b',        2, '{"context_window":131072,"supports_tools":true}',                         0.075,  0.30, false),
    ('cheap-bulk',       'groq',     'openai/gpt-oss-20b',        2, '{"context_window":131072,"supports_tools":true}',                         0.075,  0.30, false)
ON CONFLICT (profile_name, provider, model_id) DO NOTHING;

COMMIT;
