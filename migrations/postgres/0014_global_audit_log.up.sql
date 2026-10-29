-- Name: 0014_global_audit_log
-- Description: Transform the generic audit_log (migration 0007) into a tamper-evident
--   global audit log for all agent actions (issue #14).
-- References: SYSTEM_DESIGN.md §11.1(4), §10.4; ADR-010, ADR-013; NFR-5, NFR-7
--
-- audit_log was created in 0007 as a plain event table. Issue #14 turns it
-- into the single tamper-evident log for every agent action. Released
-- migrations are never rewritten, so the shape is migrated in place.

BEGIN;

-- Guard: audit_log is created by migration 0007. Issue #14 transforms it into a
-- tamper-evident log whose hash chain can only start from an empty table.
-- Adding a NOT NULL column to an existing row set would either fail
-- (PostgreSQL rejects ADD COLUMN ... NOT NULL on non-empty tables) or
-- require a non-deterministic backfill that breaks chain integrity.
-- Fail fast instead of silently corrupting history.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'audit_log'
    ) THEN
        IF EXISTS (SELECT 1 FROM public.audit_log LIMIT 1) THEN
            RAISE EXCEPTION
                'Migration 0014 requires empty audit_log: found % existing rows. Remove the rows manually before re-running.',
                (SELECT COUNT(*) FROM public.audit_log);
        END IF;
    END IF;
END $$;

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
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN hash TEXT NOT NULL;

-- 5. Query paths required by the run console.
CREATE INDEX idx_audit_log_workspace_action ON audit_log (workspace_id, action, created_at);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_id) WHERE resource_id IS NOT NULL;

-- 6. Tamper-evidence: unique hash prevents any rewritten or duplicate row.
CREATE UNIQUE INDEX idx_audit_log_hash ON audit_log (hash);
CREATE UNIQUE INDEX idx_audit_log_idempotency
    ON audit_log (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- 7. RLS policy renamed to match the issue contract.
DROP POLICY workspace_isolation_policy ON audit_log;
CREATE POLICY audit_log_isolation ON audit_log
    USING (workspace_id = current_setting('app.current_workspace_id', true)::uuid)
    WITH CHECK (workspace_id = current_setting('app.current_workspace_id', true)::uuid);

COMMIT;
