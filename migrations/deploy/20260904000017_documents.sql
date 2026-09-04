-- Deploy documents
BEGIN;

-- Documents (ADR-0022): files attached to a workflow, optionally to one of its
-- task instances. The blob lives in object storage behind platform/storage;
-- Postgres holds the metadata and the version history. A document row carries
-- current_version; every upload is an immutable document_versions row and a new
-- version is current_version + 1 in the same transaction, so exactly one
-- "latest" exists per document. Deletion is a SOFT delete (deleted_at):
-- versions and blobs are retained for the ADR-0007 purge / legal-hold
-- machinery (follow-up).
--
-- Tenant-scoped, RLS ENABLE + FORCE, HASH(tenant_id) partitioned (ADR-0020),
-- composite (tenant_id, id) PK. Every cross-table FK is composite so a
-- cross-tenant reference fails the FK check (FK validation bypasses RLS).
-- task_instance_id uses column-targeted SET NULL (PG15+): losing the instance
-- unlinks the document, it does not orphan the tenant key.
CREATE TABLE documents (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    workflow_id UUID NOT NULL,
    task_instance_id UUID,
    category VARCHAR(20) NOT NULL DEFAULT 'compliance'
        CHECK (category IN ('compliance', 'project')),
    -- Fixed vocabulary (mirrors the frontend's workflowDocumentTypeValues verbatim).
    document_type VARCHAR(40) NOT NULL
        CHECK (document_type IN (
            'draft_return', 'final_return', 'payment_confirmation', 'advisor_memo', 'workings',
            'supporting_docs', 'working_papers', 'correspondence', 'deliverable', 'other')),
    label TEXT,
    notes TEXT,
    current_version INT NOT NULL DEFAULT 1 CHECK (current_version >= 1),
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Actor attribution (ADR-0008 actor-by-ID), defaulted from the app.user_id GUC.
    created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, task_instance_id) REFERENCES task_instances(tenant_id, id)
        ON DELETE SET NULL (task_instance_id)
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('documents', 16);

CREATE INDEX idx_documents_workflow ON documents(tenant_id, workflow_id);
CREATE INDEX idx_documents_task_instance ON documents(tenant_id, task_instance_id);
CREATE INDEX idx_documents_live_created ON documents(tenant_id, created_at DESC) WHERE deleted_at IS NULL;

ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON documents
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Immutable version rows: one per upload. storage_key is the opaque,
-- tenant-prefixed object key (tenants/<tenant>/documents/<document>/<version>);
-- sha256 + file_size are computed by the API while streaming the upload.
CREATE TABLE document_versions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    document_id UUID NOT NULL,
    version INT NOT NULL CHECK (version >= 1),
    storage_key TEXT NOT NULL,
    file_name TEXT NOT NULL,
    file_size BIGINT NOT NULL CHECK (file_size >= 0),
    mime_type TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, document_id, version),
    FOREIGN KEY (tenant_id, document_id) REFERENCES documents(tenant_id, id) ON DELETE CASCADE
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('document_versions', 16);

ALTER TABLE document_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE document_versions FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON document_versions
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
