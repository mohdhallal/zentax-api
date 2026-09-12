-- Revert attested_delete_guard
BEGIN;

DROP TRIGGER IF EXISTS refuse_delete_with_attested_work ON workflow_tasks;
DROP TRIGGER IF EXISTS refuse_delete_with_attested_work ON workflows;
DROP FUNCTION IF EXISTS refuse_delete_with_attested_work();

COMMIT;
