-- Deploy entities
BEGIN;

-- Entities: tax-paying orgs, hierarchical via parent_entity_id. The first
-- tenant-scoped table — the template every later domain table follows.
--
-- tenant_id defaults to the app.tenant_id GUC bound at the Tx seam (ADR-0004),
-- so INSERTs never spell it out; a tenant-less transaction leaves the GUC unset
-- → the default is NULL → NOT NULL fails closed.
CREATE TABLE entities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    parent_entity_id UUID REFERENCES entities(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    legal_name TEXT,
    country TEXT NOT NULL,
    tax_residency TEXT,
    fiscal_calendar_pattern VARCHAR(20) NOT NULL DEFAULT 'standard',
    financial_year_end VARCHAR(5), -- MM-DD, a legal date-only value (ADR-0002)
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_entities_tenant_id ON entities(tenant_id);
CREATE INDEX idx_entities_parent ON entities(parent_entity_id);

-- Row-Level Security (ADR-0004): every row is isolated by the app.tenant_id
-- GUC. FORCE applies the policy even to the table owner, so the app cannot see
-- across tenants even if it connects as the owner. current_setting(..., true)
-- returns NULL when unset → the comparison denies → fail closed.
ALTER TABLE entities ENABLE ROW LEVEL SECURITY;
ALTER TABLE entities FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON entities
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
