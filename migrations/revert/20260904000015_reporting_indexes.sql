-- Revert reporting_indexes
BEGIN;

-- Dropping the parent index drops the propagated partition indexes with it.
DROP INDEX IF EXISTS idx_task_instances_due_date;
DROP INDEX IF EXISTS idx_task_instances_workflow_period;
CREATE INDEX IF NOT EXISTS idx_task_instances_workflow ON task_instances (workflow_id);
DROP INDEX IF EXISTS idx_workflows_financial_year;
DROP INDEX IF EXISTS idx_workflows_status;

COMMIT;
