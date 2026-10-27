BEGIN;

ALTER TABLE task_runs ADD COLUMN workspace_path TEXT;

COMMIT;
