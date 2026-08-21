-- Deploy entity_obligations
BEGIN;

-- Entity obligations link an entity to an obligation type with jurisdiction,
-- periodicity, and a deadline-rule config (JSONB — the rule shape evolves as
-- data, ADR-0017, so it is not pinned to columns). The entity/obligation FKs are
-- COMPOSITE on (tenant_id, id): Postgres FK checks bypass RLS, so an id-only FK
-- would admit a cross-tenant reference; including tenant_id makes a cross-tenant
-- id fail the FK check (→ 400) instead of silently linking across tenants.
CREATE TABLE entity_obligations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL,
    obligation_type_id UUID NOT NULL,
    jurisdiction TEXT,
    periodicity VARCHAR(30) NOT NULL, -- monthly | quarterly | bi-annual | annual | consolidated-annual
    deadline_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (tenant_id, entity_id) REFERENCES entities(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, obligation_type_id) REFERENCES obligation_types(tenant_id, id) ON DELETE RESTRICT
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
