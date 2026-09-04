-- Deploy reporting_indexes
BEGIN;

-- Supporting indexes for the SQL-backed reports (ADR-0021 rule 3: index for
-- the shape, in the migration that introduces the query). Each index names
-- the statement it serves:
--   task_instances (due_date)                — the raw-export date window
--                                              (range predicate on due_date) and
--                                              the dueDate-ordered task view;
--   task_instances (workflow_id, period_code) — per-workflow, per-period reads
--                                              (stats grouping, instance lists);
--                                              it REPLACES idx_task_instances_workflow,
--                                              which was a strict prefix of it;
--   workflows (financial_year)               — the `year` report filter;
--   workflows (status)                       — the active/completed participation
--                                              rule of every compliance report.
-- The heatmap / compliance-status aggregates scan the tenant's own partition
-- (RLS predicate = whole partition) and need no extra index at pilot scale;
-- re-check with EXPLAIN when a tenant's partition grows (ADR-0021 stage 1).
-- Both tables are HASH(tenant_id) partitioned (ADR-0020): an index created on
-- the partitioned parent is propagated to every existing partition (and to any
-- partition attached later), so one tenant's report touches one partition's
-- index, never the whole table's.
CREATE INDEX idx_task_instances_due_date ON task_instances (due_date);
CREATE INDEX idx_task_instances_workflow_period ON task_instances (workflow_id, period_code);
DROP INDEX idx_task_instances_workflow;
CREATE INDEX idx_workflows_financial_year ON workflows (financial_year);
CREATE INDEX idx_workflows_status ON workflows (status);

COMMIT;
