-- Deploy data_templates
BEGIN;

-- Data templates are reusable sets of typed fields (text / numeric / date /
-- boolean / file, with numeric validation + currency formatting) that a task
-- instance's tax data is collected against. The field list is a JSONB array
-- (validated strictly by the application — the frontend's DataField shape).
-- category = 'predefined' rows are the curated VAT / CIT / WHT templates,
-- seeded per tenant and immutable through the API; 'custom' rows are the
-- tenant's own. name is unique per tenant.
-- Tenant-scoped, partitioned by HASH(tenant_id) (ADR-0020), composite PK
-- (tenant_id, id) — also the composite FK target for workflow_tasks and
-- task_instances below (FK checks bypass RLS, so an id-only FK would admit a
-- cross-tenant template).
CREATE TABLE data_templates (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    template_type VARCHAR(20) NOT NULL CHECK (template_type IN ('VAT', 'CIT', 'TP', 'WHT', 'Custom')),
    category VARCHAR(20) NOT NULL DEFAULT 'custom' CHECK (category IN ('predefined', 'custom')),
    description TEXT,
    fields JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, name)
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('data_templates', 16);

CREATE INDEX idx_data_templates_category ON data_templates(category);

ALTER TABLE data_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_templates FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON data_templates
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- The template references that were "plain nullable UUIDs with no FK" until
-- now become composite FKs. Column-targeted ON DELETE SET NULL (Postgres 15+)
-- clears only the pointer, never the tenant_id; the application refuses to
-- delete a template that is still referenced (409) before this ever fires.
ALTER TABLE workflow_tasks
    ADD CONSTRAINT workflow_tasks_data_template_fk
    FOREIGN KEY (tenant_id, data_template_id) REFERENCES data_templates(tenant_id, id)
    ON DELETE SET NULL (data_template_id);

ALTER TABLE task_instances
    ADD CONSTRAINT task_instances_data_template_fk
    FOREIGN KEY (tenant_id, data_template_id) REFERENCES data_templates(tenant_id, id)
    ON DELETE SET NULL (data_template_id);

CREATE INDEX idx_workflow_tasks_data_template ON workflow_tasks(data_template_id);
CREATE INDEX idx_task_instances_data_template ON task_instances(data_template_id);

COMMIT;
