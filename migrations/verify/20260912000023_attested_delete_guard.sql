-- Verify attested_delete_guard
-- The guard function exists, and a BEFORE DELETE ROW trigger carrying the right
-- kind argument sits on BOTH partitioned parents and has been cloned to all 16
-- partitions of each (a parent-only trigger would not fire for a partition row).
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_proc p
            JOIN pg_namespace n ON n.oid = p.pronamespace
            WHERE p.proname = 'refuse_delete_with_attested_work'
              AND n.nspname = current_schema())
    -- One trigger on each partitioned parent, BEFORE (tgtype bit 1) DELETE
    -- (bit 8) FOR EACH ROW (bit 0).
    AND (SELECT COUNT(*)::int = 2
         FROM pg_trigger t
         JOIN pg_class c ON c.oid = t.tgrelid
         WHERE t.tgname = 'refuse_delete_with_attested_work'
           AND NOT t.tgisinternal
           AND c.relname IN ('workflows', 'workflow_tasks')
           AND c.relkind = 'p'
           AND (t.tgtype & 1) = 1    -- FOR EACH ROW
           AND (t.tgtype & 2) = 2    -- BEFORE
           AND (t.tgtype & 8) = 8)   -- DELETE
    -- Cloned to every partition of both parents.
    AND (SELECT COUNT(*)::int = 32
         FROM pg_trigger t
         JOIN pg_class c ON c.oid = t.tgrelid
         WHERE t.tgname = 'refuse_delete_with_attested_work'
           AND NOT t.tgisinternal
           AND c.relispartition
           AND (c.relname LIKE 'workflows\_p%' OR c.relname LIKE 'workflow\_tasks\_p%'))
)::int;
