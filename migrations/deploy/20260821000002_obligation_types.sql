-- Deploy obligation_types
BEGIN;

-- Obligation types: tax obligation definitions (VAT/CIT/TP/WHT/Custom), either
-- curated-predefined or tenant-custom. Tenant-scoped like entities; code is
-- unique per tenant. (Globally-shared curated content is a later concern —
-- ADR-0017 tax-rule versioning.)
CREATE TABLE obligation_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    code TEXT NOT NULL,
    category VARCHAR(20) NOT NULL DEFAULT 'custom', -- 'predefined' | 'custom'
    template VARCHAR(20) NOT NULL,                  -- 'VAT' | 'CIT' | 'TP' | 'WHT' | 'Custom'
    status VARCHAR(20) NOT NULL DEFAULT 'active',   -- 'active' | 'inactive'
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, code)
);

CREATE INDEX idx_obligation_types_tenant_id ON obligation_types(tenant_id);

ALTER TABLE obligation_types ENABLE ROW LEVEL SECURITY;
ALTER TABLE obligation_types FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON obligation_types
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
