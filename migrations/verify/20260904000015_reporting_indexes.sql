-- Verify reporting_indexes
-- Each new index must exist on the partitioned parent and the replaced prefix
-- index must be gone (1/0 fails the script otherwise).
-- idx_task_instances_due_date is itself superseded by 20260908000022
-- (idx_task_instances_tenant_due, tenant-prefixed); either one satisfies this
-- check so a whole-plan `sqitch verify` stays green after 22 is deployed.
SELECT 1 / (
    (SELECT COUNT(*)::int = 3
     FROM pg_indexes
     WHERE schemaname = current_schema()
       AND indexname IN (
           'idx_task_instances_workflow_period',
           'idx_workflows_financial_year',
           'idx_workflows_status'
       ))
    AND EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema()
          AND indexname IN ('idx_task_instances_due_date', 'idx_task_instances_tenant_due')
    )
    AND NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'idx_task_instances_workflow'
    )
)::int;
