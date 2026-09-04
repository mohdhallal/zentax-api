-- Revert data_templates
BEGIN;

DROP INDEX IF EXISTS idx_task_instances_data_template;
DROP INDEX IF EXISTS idx_workflow_tasks_data_template;
ALTER TABLE task_instances DROP CONSTRAINT IF EXISTS task_instances_data_template_fk;
ALTER TABLE workflow_tasks DROP CONSTRAINT IF EXISTS workflow_tasks_data_template_fk;

-- Dropping the partitioned parent drops every partition with it.
DROP TABLE IF EXISTS data_templates;

COMMIT;
