-- Deploy workflow_tasks
BEGIN;

-- Workflow tasks are ordered task TEMPLATES on a workflow. Task instances are
-- generated from these per period when a workflow starts (a later migration).
-- Tenant-scoped; the workflow_id FK is COMPOSITE on (tenant_id, id) so a cross-
-- tenant reference fails the FK check (FK validation bypasses RLS). The flat
-- due_date_* columns are the per-task offset rule the generator will apply.
CREATE TABLE workflow_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    workflow_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    task_type VARCHAR(30) NOT NULL, -- data_request | review | preparation | submission | payment | approval | other
    role_label TEXT,
    approval_required BOOLEAN NOT NULL DEFAULT false,
    due_date_reference VARCHAR(20) NOT NULL DEFAULT 'filing_deadline', -- filing_deadline | period_end
    due_date_offset_value SMALLINT NOT NULL DEFAULT 0,
    due_date_offset_unit VARCHAR(10) NOT NULL DEFAULT 'days',          -- days | weeks | months
    due_date_offset_direction VARCHAR(10) NOT NULL DEFAULT 'before',   -- before | after
    order_index SMALLINT NOT NULL DEFAULT 0,
    data_template_id UUID, -- optional; no FK yet (data_templates not built)
    required_documents JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, id), -- composite FK target for task_instances
    FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_tasks_tenant_id ON workflow_tasks(tenant_id);
CREATE INDEX idx_workflow_tasks_workflow ON workflow_tasks(workflow_id);

ALTER TABLE workflow_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_tasks FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON workflow_tasks
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
