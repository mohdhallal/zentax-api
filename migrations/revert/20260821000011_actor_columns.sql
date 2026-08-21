-- Revert actor_columns
BEGIN;

ALTER TABLE entities DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;
ALTER TABLE obligation_types DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;
ALTER TABLE entity_obligations DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;
ALTER TABLE workflows DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;
ALTER TABLE workflow_tasks DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;
ALTER TABLE task_instances DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS updated_by;

COMMIT;
