-- Verify storage_reclaim
SELECT id, tenant_id, storage_key, reason, resource_type, resource_id, queued_at, reclaimed_at
FROM storage_reclaim
WHERE false;

-- RLS enabled AND forced (the app connects as the table owner in the
-- self-host edition), the pending-work index present, and no foreign key
-- pointing at documents / document_versions — the queue must survive them.
SELECT 1 / (
    (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'storage_reclaim')
    AND EXISTS (SELECT 1 FROM pg_policies
                WHERE schemaname = current_schema()
                  AND tablename = 'storage_reclaim'
                  AND policyname = 'tenant_isolation')
    AND EXISTS (SELECT 1 FROM pg_indexes
                WHERE schemaname = current_schema()
                  AND indexname = 'idx_storage_reclaim_pending'
                  AND indexdef LIKE '%WHERE (reclaimed_at IS NULL)')
    AND (SELECT COUNT(*)::int = 1
         FROM pg_constraint
         WHERE conrelid = 'storage_reclaim'::regclass AND contype = 'f')
)::int;
