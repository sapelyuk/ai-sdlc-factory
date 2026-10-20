-- Name: 0009_init_users (down)
-- Description: drop users table

BEGIN;

DROP TRIGGER IF EXISTS update_users_updated_at ON users;
DROP FUNCTION IF EXISTS users_update_updated_at_column();
DROP POLICY IF EXISTS workspace_isolation_policy ON users;
DROP TABLE IF EXISTS users;

COMMIT;
