-- Revert workflow_tasks
BEGIN;

DROP TABLE IF EXISTS workflow_tasks;

COMMIT;
