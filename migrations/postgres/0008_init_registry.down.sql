-- Name: 0008_init_registry
-- Description: rollback registry tables
-- References: SYSTEM_DESIGN.md §4.3

BEGIN;

DROP TRIGGER IF EXISTS update_processes_updated_at ON processes;
DROP TRIGGER IF EXISTS update_teams_updated_at ON teams;
DROP TRIGGER IF EXISTS update_roles_updated_at ON roles;

DROP TABLE IF EXISTS processes CASCADE;
DROP TABLE IF EXISTS teams CASCADE;
DROP TABLE IF EXISTS roles CASCADE;

DROP FUNCTION IF EXISTS registry_update_updated_at_column();

COMMIT;
