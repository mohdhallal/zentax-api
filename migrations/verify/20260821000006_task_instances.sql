-- Verify task_instances
SELECT id, tenant_id, workflow_id, workflow_task_id, period_code, name, description, task_type,
       status, assignee_id, due_date, period_end_date, filing_deadline, approval_required,
       approved_by, approved_at, completed_at, order_index, notes, data_template_id,
       tax_data, tax_data_status, created_at, updated_at
FROM task_instances
WHERE false;
