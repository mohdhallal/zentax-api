-- Revert task_instances
BEGIN;

DROP TABLE IF EXISTS task_instances;

COMMIT;
