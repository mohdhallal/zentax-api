-- Verify pagination_indexes
-- Both new indexes must exist on the partitioned parent (the second with its
-- partial predicate), the two replaced indexes must be gone, and each parent
-- index must have propagated to exactly the 16 hash partitions with every
-- partition index valid (1/0 fails the script otherwise).
SELECT 1 / (
    (SELECT COUNT(*)::int = 2
     FROM pg_indexes
     WHERE schemaname = current_schema()
       AND tablename = 'task_instances'
       AND indexname IN ('idx_task_instances_tenant_due', 'idx_task_instances_tenant_open_due'))
    AND EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema()
          AND indexname = 'idx_task_instances_tenant_open_due'
          AND indexdef LIKE '%(tenant_id, due_date, order_index, id) WHERE %status%<>%completed%'
    )
    AND EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema()
          AND indexname = 'idx_task_instances_tenant_due'
          AND indexdef LIKE '%(tenant_id, due_date, order_index, id)'
    )
    AND NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema()
          AND indexname IN ('idx_task_instances_due_date', 'idx_task_instances_status')
    )
    -- Propagation: 16 valid partition indexes under each parent index.
    AND (SELECT COUNT(*)::int = 16
         FROM pg_inherits inh
         JOIN pg_class child ON child.oid = inh.inhrelid
         JOIN pg_index ix ON ix.indexrelid = child.oid
         JOIN pg_indexes pi ON pi.indexname = child.relname AND pi.schemaname = current_schema()
         WHERE inh.inhparent = (SELECT c.oid FROM pg_class c
                                JOIN pg_namespace n ON n.oid = c.relnamespace
                                WHERE c.relname = 'idx_task_instances_tenant_due'
                                  AND n.nspname = current_schema())
           AND pi.tablename LIKE 'task_instances_p%'
           AND ix.indisvalid)
    AND (SELECT COUNT(*)::int = 16
         FROM pg_inherits inh
         JOIN pg_class child ON child.oid = inh.inhrelid
         JOIN pg_index ix ON ix.indexrelid = child.oid
         JOIN pg_indexes pi ON pi.indexname = child.relname AND pi.schemaname = current_schema()
         WHERE inh.inhparent = (SELECT c.oid FROM pg_class c
                                JOIN pg_namespace n ON n.oid = c.relnamespace
                                WHERE c.relname = 'idx_task_instances_tenant_open_due'
                                  AND n.nspname = current_schema())
           AND pi.tablename LIKE 'task_instances_p%'
           AND ix.indisvalid
           AND pi.indexdef LIKE '%WHERE %status%<>%completed%')
)::int;
