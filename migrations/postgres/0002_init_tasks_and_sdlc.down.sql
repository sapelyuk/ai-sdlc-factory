-- Name: 0002_init_tasks_and_sdlc
-- Description: Rollback runs, stage_runs, task_runs, approvals
-- Order: reverse numeric (0007 → 0001)

BEGIN;

DROP TABLE IF EXISTS approvals CASCADE;
DROP TABLE IF EXISTS task_runs CASCADE;
DROP TABLE IF EXISTS stage_runs CASCADE;
DROP TABLE IF EXISTS runs CASCADE;

COMMIT;
