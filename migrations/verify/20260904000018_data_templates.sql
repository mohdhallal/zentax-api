-- Verify data_templates
SELECT id, tenant_id, name, template_type, category, description, fields,
       created_at, updated_at, created_by, updated_by
FROM data_templates
WHERE false;

-- The parent must be hash-partitioned and both composite FKs must exist.
SELECT 1 / (
    (SELECT relkind = 'p' FROM pg_class WHERE relname = 'data_templates')
    AND (SELECT COUNT(*) = 1 FROM pg_constraint WHERE conname = 'workflow_tasks_data_template_fk' AND conrelid = 'workflow_tasks'::regclass)
    AND (SELECT COUNT(*) = 1 FROM pg_constraint WHERE conname = 'task_instances_data_template_fk' AND conrelid = 'task_instances'::regclass)
)::int;
