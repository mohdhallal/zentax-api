-- Deploy pagination_indexes
BEGIN;

-- Pagination indexes (ADR-0021 rules 3 and 7; ADR-0026): offset pages over
-- the task feed must be deterministic and cost O(page) for one tenant — never
-- O(offset) through the rows of every other tenant sharing the hash partition,
-- which is what the tenant-less (due_date) index bought. Each index names the
-- statement it serves:
--   task_instances (tenant_id, due_date, order_index, id)
--       — the feed's default order: GET /task-instances (ORDER BY due_date,
--         id) and GET /reports/task-instances sort=dueDate (ORDER BY due_date,
--         order_index, id) with LIMIT/OFFSET; the due-window predicates
--         (overdue / due today / this week, dueFrom/dueTo) and the raw-export
--         date window. The tenant prefix matches the RLS predicate, so the scan
--         starts at the tenant's first row instead of filtering everyone
--         else's. It REPLACES idx_task_instances_due_date (migration 15), which
--         omitted the tenant.
--   task_instances (tenant_id, due_date, order_index, id) WHERE status <> 'completed'
--       — open-work reads: the dashboard's soonest open tasks and every
--         status=open feed page must not walk years of completed rows to find
--         the next five. It REPLACES idx_task_instances_status (migration 06):
--         a single-column index on a six-value enum, never chosen over the
--         partition scan and useless for ordering.
-- The remaining idx_task_instances_workflow_period / _workflow_task indexes
-- serve the per-workflow reads and stay.
--
-- Lock note: a plain transactional CREATE INDEX holds a SHARE lock on
-- task_instances and each of its 16 partitions for the build (writes block,
-- reads continue) and the DROPs take ACCESS EXCLUSIVE for an instant. Nothing
-- is deployed yet, so this is a convention, not a live lock window: once any
-- cell's task_instances exceeds ~10^6 rows (or a build measures > 30 s), index
-- migrations switch to CREATE INDEX ... ON ONLY the parent + per-partition
-- CONCURRENTLY + ALTER INDEX ... ATTACH PARTITION in a NON-transactional,
-- idempotent file (migrate.sh runs files with plain psql -f, so that shape
-- needs no runner change).
-- task_instances is HASH(tenant_id) partitioned (ADR-0020): an index created
-- on the partitioned parent propagates to every existing partition (and to any
-- partition attached later), so one tenant's page touches one partition's
-- index, never the whole table's.
CREATE INDEX idx_task_instances_tenant_due
    ON task_instances (tenant_id, due_date, order_index, id);
CREATE INDEX idx_task_instances_tenant_open_due
    ON task_instances (tenant_id, due_date, order_index, id)
    WHERE status <> 'completed';
DROP INDEX idx_task_instances_due_date;
DROP INDEX idx_task_instances_status;

COMMIT;
