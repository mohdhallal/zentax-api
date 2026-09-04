-- Verify reporting_indexes
-- Each new index must exist on the partitioned parent and the replaced prefix
-- index must be gone (1/0 fails the script otherwise).
SELECT 1 / (
    (SELECT COUNT(*)::int = 4
     FROM pg_indexes
     WHERE schemaname = current_schema()
       AND indexname IN (
           'idx_task_instances_due_date',
           'idx_task_instances_workflow_period',
           'idx_workflows_financial_year',
           'idx_workflows_status'
       ))
    AND NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'idx_task_instances_workflow'
    )
)::int;
