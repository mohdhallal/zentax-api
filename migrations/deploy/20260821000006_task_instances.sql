-- Deploy task_instances
BEGIN;

-- Task instances are the actual per-period tasks generated from workflow task
-- templates when a workflow is started. due_date / period_end_date /
-- filing_deadline are DATE (legal date-only, ADR-0002 — never a timestamp).
-- Tenant-scoped; the workflow_id / workflow_task_id FKs are COMPOSITE on
-- (tenant_id, id) so a cross-tenant reference fails the FK check (bypasses RLS).
CREATE TABLE task_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    workflow_id UUID NOT NULL,
    workflow_task_id UUID NOT NULL,
    period_code VARCHAR(10) NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    task_type VARCHAR(30) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'not_started',
    assignee_id UUID,
    due_date DATE NOT NULL,
    period_end_date DATE NOT NULL,
    filing_deadline DATE NOT NULL,
    approval_required BOOLEAN NOT NULL DEFAULT false,
    approved_by UUID,
    approved_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    order_index SMALLINT NOT NULL DEFAULT 0,
    notes TEXT,
    data_template_id UUID,
    tax_data JSONB,
    tax_data_status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, workflow_task_id) REFERENCES workflow_tasks(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_task_instances_tenant_id ON task_instances(tenant_id);
CREATE INDEX idx_task_instances_workflow ON task_instances(workflow_id);
CREATE INDEX idx_task_instances_workflow_task ON task_instances(workflow_task_id);
CREATE INDEX idx_task_instances_status ON task_instances(status);

ALTER TABLE task_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_instances FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON task_instances
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
