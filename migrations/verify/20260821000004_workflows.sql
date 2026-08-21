-- Verify workflows
SELECT id, tenant_id, name, description, workflow_category, project_type, financial_year,
       periodicity, selected_periods, entity_id, obligation_type_id, due_date_rule,
       start_date, end_date, tasks_sequential, status, created_at, updated_at
FROM workflows
WHERE false;
