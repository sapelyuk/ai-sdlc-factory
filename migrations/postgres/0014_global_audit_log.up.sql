-- Name: 0014_global_audit_log
-- Description: Transform the generic audit_log (migration 0007) into a tamper-evident
--   global audit log for all agent actions (issue #14).
-- References: SYSTEM_DESIGN.md §11.1(4), §10.4; ADR-010, ADR-013; NFR-5, NFR-7
--
-- audit_log was created in 0007 as a plain event table. Issue #14 turns it
-- into the single tamper-evident log for every agent action. Released
-- migrations are never rewritten, so the shape is migrated in place.

BEGIN;

-- 1. resource_id identifies heterogeneous resources (run id, task id,
--    container id), so it becomes free-form text.
ALTER TABLE audit_log
    ALTER COLUMN resource_id TYPE TEXT USING resource_id::text,
    ALTER COLUMN resource_id DROP NOT NULL;

-- 2. The old resource_type / risk_level / result triple is not part of the
--    global event contract; the same information now lives in details JSONB.
ALTER TABLE audit_log DROP COLUMN resource_type;
ALTER TABLE audit_log DROP COLUMN risk_level;
ALTER TABLE audit_log DROP COLUMN result;

-- 3. Audit rows must outlive the workspace they belong to: an append-only
--    evidence log cannot be cascade-deleted.
ALTER TABLE audit_log DROP CONSTRAINT audit_log_workspace_id_fkey;

-- 4. Hash chain columns (added on top of existing columns).
ALTER TABLE audit_log
    ADD COLUMN prev_hash TEXT,
    ADD COLUMN hash TEXT NOT NULL;

-- 5. Query paths required by the run console.
CREATE INDEX idx_audit_log_workspace_action ON audit_log (workspace_id, action, created_at);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_id) WHERE resource_id IS NOT NULL;

-- 6. Tamper-evidence: unique hash prevents any rewritten or duplicate row.
CREATE UNIQUE INDEX idx_audit_log_hash ON audit_log (hash);

-- 7. RLS policy renamed to match the issue contract.
DROP POLICY workspace_isolation_policy ON audit_log;
CREATE POLICY audit_log_isolation ON audit_log
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid)
    WITH CHECK (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
