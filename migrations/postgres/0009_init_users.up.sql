-- Name: 0009_init_users
-- Description: users table for JWT authentication with RBAC roles
-- References: SYSTEM_DESIGN.md §4.3, §10.3, ADR-015, ADR-013
-- NFR-5 (security)

BEGIN;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'operator', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX users_workspace_id_idx ON users (workspace_id);
CREATE INDEX users_email_idx ON users (email);

-- Enable Row Level Security
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;

CREATE POLICY workspace_isolation_policy ON users
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

-- Trigger: update updated_at
CREATE OR REPLACE FUNCTION users_update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION users_update_updated_at_column();

COMMIT;
