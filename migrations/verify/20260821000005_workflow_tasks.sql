-- Verify workflow_tasks
SELECT id, tenant_id, workflow_id, name, description, task_type, role_label, approval_required,
       due_date_reference, due_date_offset_value, due_date_offset_unit, due_date_offset_direction,
       order_index, data_template_id, required_documents, created_at, updated_at
FROM workflow_tasks
WHERE false;
