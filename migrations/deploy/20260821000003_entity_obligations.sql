-- Deploy entity_obligations
BEGIN;

-- Entity obligations link an entity to an obligation type with jurisdiction,
-- periodicity, and a deadline-rule config (JSONB — the rule shape evolves as
-- data, ADR-0017, so it is not pinned to columns). Both FKs point at
-- tenant-scoped tables, so with RLS active they can only reference rows in the
-- same tenant (a cross-tenant id is invisible → FK violation).
CREATE TABLE entity_obligations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    obligation_type_id UUID NOT NULL REFERENCES obligation_types(id) ON DELETE RESTRICT,
    jurisdiction TEXT,
    periodicity VARCHAR(30) NOT NULL, -- monthly | quarterly | bi-annual | annual | consolidated-annual
    deadline_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_entity_obligations_tenant_id ON entity_obligations(tenant_id);
CREATE INDEX idx_entity_obligations_entity ON entity_obligations(entity_id);
CREATE INDEX idx_entity_obligations_obligation_type ON entity_obligations(obligation_type_id);

ALTER TABLE entity_obligations ENABLE ROW LEVEL SECURITY;
ALTER TABLE entity_obligations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON entity_obligations
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
