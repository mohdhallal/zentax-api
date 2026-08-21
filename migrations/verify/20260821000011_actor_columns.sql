-- Verify actor_columns
SELECT created_by, updated_by FROM entities WHERE false;
SELECT created_by, updated_by FROM obligation_types WHERE false;
SELECT created_by, updated_by FROM entity_obligations WHERE false;
SELECT created_by, updated_by FROM workflows WHERE false;
SELECT created_by, updated_by FROM workflow_tasks WHERE false;
SELECT created_by, updated_by FROM task_instances WHERE false;
