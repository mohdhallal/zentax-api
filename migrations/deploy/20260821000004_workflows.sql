-- Deploy workflows
BEGIN;

-- Workflows are either 'recurring' (tied to an entity + obligation type, periods
-- auto-calculated) or 'project' (one-off). Tenant-scoped; the entity_id /
-- obligation_type_id FKs are RLS-scoped to the same tenant. selected_periods and
-- due_date_rule are JSONB (period codes + a structured filing due-date rule).
CREATE TABLE workflows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    workflow_category VARCHAR(20) NOT NULL DEFAULT 'recurring', -- recurring | project
    project_type VARCHAR(30),                                   -- dispute | audit_verification | ...
    financial_year VARCHAR(9),
    periodicity VARCHAR(30),
    selected_periods JSONB NOT NULL DEFAULT '[]'::jsonb,        -- e.g. ["M1","M2","Q1"]
    entity_id UUID REFERENCES entities(id) ON DELETE CASCADE,
    obligation_type_id UUID REFERENCES obligation_types(id) ON DELETE RESTRICT,
    due_date_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    start_date VARCHAR(10),                                     -- YYYY-MM-DD legal date (ADR-0002)
    end_date VARCHAR(10),                                       -- YYYY-MM-DD legal date (ADR-0002)
    tasks_sequential BOOLEAN NOT NULL DEFAULT false,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',               -- draft | active | completed | archived
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_workflows_tenant_id ON workflows(tenant_id);
CREATE INDEX idx_workflows_entity ON workflows(entity_id);
CREATE INDEX idx_workflows_obligation_type ON workflows(obligation_type_id);

ALTER TABLE workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflows FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON workflows
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
