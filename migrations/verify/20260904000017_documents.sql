-- Verify documents
SELECT id, tenant_id, workflow_id, task_instance_id, category, document_type, label, notes,
       current_version, deleted_at, created_at, updated_at, created_by, updated_by
FROM documents
WHERE false;

SELECT id, tenant_id, document_id, version, storage_key, file_name, file_size, mime_type, sha256,
       created_at, created_by
FROM document_versions
WHERE false;

-- Both parents must be hash-partitioned with RLS forced.
SELECT 1 / (
    (SELECT relkind = 'p' AND relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'documents')
    AND (SELECT relkind = 'p' AND relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'document_versions')
    AND (SELECT to_regclass('documents_p00') IS NOT NULL)
    AND (SELECT to_regclass('document_versions_p15') IS NOT NULL)
)::int;
