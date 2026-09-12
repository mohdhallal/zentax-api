-- Deploy storage_reclaim
BEGIN;

-- The blob-reclaim queue (ADR-0007 purge, ADR-0022 storage seam).
--
-- A document's bytes live in object storage; Postgres holds only the metadata.
-- `documents` is soft-deleted (deleted_at) precisely so the purge job can find
-- the blob later and remove it — but documents cascade from workflows, and
-- workflows from entities, so a legitimate workflow / entity delete physically
-- removes the metadata rows and with them the only handle anyone had on those
-- objects. The bytes would then sit in the bucket forever, unreferenced and
-- unfindable: a GDPR erasure that never completes and a bill nobody can explain.
--
-- So the delete use cases copy every affected document_versions.storage_key
-- into this queue ON THE SAME TRANSACTION as the delete: commit together, roll
-- back together. Nothing here deletes an object — the purge job (ADR-0007,
-- still to be built) drains the queue and stamps reclaimed_at. Deleting blobs
-- inline would be the one step a rollback could not undo.
--
-- Deliberately NOT partitioned and deliberately FK-free beyond the tenant: the
-- rows must OUTLIVE the documents they name (that is their whole purpose), and
-- the queue is small and drained (the ADR-0020 exception that user_grants and
-- entity_closure already take). tenant_id still cascades from tenants, so
-- removing a tenant wholesale takes its queue with it.
CREATE TABLE storage_reclaim (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    -- The audit action that queued the row: workflow.deleted | entity.deleted.
    reason VARCHAR(40) NOT NULL,
    -- The deleted resource, so a queue row can be traced back to its audit entry.
    resource_type VARCHAR(20) NOT NULL,
    resource_id UUID NOT NULL,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reclaimed_at TIMESTAMPTZ
);

-- The purge job's working set: this tenant's still-unreclaimed keys, oldest first.
CREATE INDEX idx_storage_reclaim_pending ON storage_reclaim (tenant_id, queued_at)
    WHERE reclaimed_at IS NULL;

ALTER TABLE storage_reclaim ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_reclaim FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON storage_reclaim
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
