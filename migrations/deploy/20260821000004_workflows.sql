-- Deploy workflows
BEGIN;

-- Workflows are either 'recurring' (tied to an entity + obligation type, periods
-- auto-calculated) or 'project' (one-off). Tenant-scoped; the entity_id /
-- obligation_type_id FKs are COMPOSITE on (tenant_id, id) so a cross-tenant
-- reference fails the FK check (FK validation bypasses RLS). selected_periods and
-- due_date_rule are JSONB (period codes + a structured filing due-date rule).
-- Partitioned by HASH(tenant_id) (ADR-0020); composite PK (tenant_id, id) is
-- also the FK target for workflow_tasks / task_instances.
CREATE TABLE workflows (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    workflow_category VARCHAR(20) NOT NULL DEFAULT 'recurring', -- recurring | project
    project_type VARCHAR(30),                                   -- dispute | audit_verification | ...
    financial_year VARCHAR(9),
    periodicity VARCHAR(30),
    selected_periods JSONB NOT NULL DEFAULT '[]'::jsonb,        -- e.g. ["M1","M2","Q1"]
    entity_id UUID,
    obligation_type_id UUID,
    due_date_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    start_date VARCHAR(10),                                     -- YYYY-MM-DD legal date (ADR-0002)
    end_date VARCHAR(10),                                       -- YYYY-MM-DD legal date (ADR-0002)
    tasks_sequential BOOLEAN NOT NULL DEFAULT false,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',               -- draft | active | completed | archived
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, entity_id) REFERENCES entities(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, obligation_type_id) REFERENCES obligation_types(tenant_id, id) ON DELETE RESTRICT
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('workflows', 16);

CREATE INDEX idx_workflows_entity ON workflows(entity_id);
CREATE INDEX idx_workflows_obligation_type ON workflows(obligation_type_id);

ALTER TABLE workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflows FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON workflows
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
