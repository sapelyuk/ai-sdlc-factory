-- Ensures the local seed workspace exists before `make seed-slp` loads SLP v0.
-- sem_nodes.workspace_id references workspaces(id), so the workspace must be
-- present. Idempotent: safe to run on every seed.
-- Passed from the Makefile: -v workspace_id=<SEED_WORKSPACE_ID>
INSERT INTO workspaces (id, name, owner_id)
VALUES (:'workspace_id'::uuid, 'default', 'seed')
ON CONFLICT (id) DO NOTHING;
