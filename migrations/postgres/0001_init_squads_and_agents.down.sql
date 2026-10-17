-- Name: 0001_init_squads_and_agents
-- Description: Rollback workspaces, squads, agents, model_profiles
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS agents CASCADE;
DROP TABLE IF EXISTS model_profiles CASCADE;
DROP TABLE IF EXISTS squads CASCADE;
DROP TABLE IF EXISTS workspaces CASCADE;

COMMIT;
