-- Revert pagination_indexes
BEGIN;

-- Dropping the parent index drops the propagated partition indexes with it;
-- the two indexes this migration replaced come back as they were.
DROP INDEX IF EXISTS idx_task_instances_tenant_due;
DROP INDEX IF EXISTS idx_task_instances_tenant_open_due;
CREATE INDEX IF NOT EXISTS idx_task_instances_due_date ON task_instances (due_date);
CREATE INDEX IF NOT EXISTS idx_task_instances_status ON task_instances (status);

COMMIT;
