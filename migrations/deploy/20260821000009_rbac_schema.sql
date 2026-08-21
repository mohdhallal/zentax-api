-- Deploy rbac_schema
BEGIN;

-- Scoped-RBAC schema (ADR-0012), created now so scope isn't retrofitted onto a
-- flat role later. ENFORCEMENT lands in a follow-up increment; for now the CLI
-- seed writes one tenant-admin grant per bootstrap user.

-- A grant = (user, role, scope). scope_entity_id NULL means tenant-wide;
-- otherwise the role applies to that entity subtree (via entity_closure).
CREATE TABLE user_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(30) NOT NULL, -- tenant_admin | manager | reviewer | preparer | viewer
    scope_entity_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- composite FK so a grant can't scope to another tenant's entity (FK bypasses RLS)
    FOREIGN KEY (tenant_id, scope_entity_id) REFERENCES entities(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_user_grants_tenant_id ON user_grants(tenant_id);
CREATE INDEX idx_user_grants_user ON user_grants(user_id);

ALTER TABLE user_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_grants
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Entity closure table for cheap ancestor/descendant scope checks. Maintenance
-- (rows on entity create/reparent) + scope queries land with RBAC enforcement.
CREATE TABLE entity_closure (
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    ancestor_id UUID NOT NULL,
    descendant_id UUID NOT NULL,
    depth SMALLINT NOT NULL,
    PRIMARY KEY (ancestor_id, descendant_id),
    -- composite FKs: closure rows can't span tenants (FK bypasses RLS)
    FOREIGN KEY (tenant_id, ancestor_id) REFERENCES entities(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, descendant_id) REFERENCES entities(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_entity_closure_tenant_id ON entity_closure(tenant_id);
CREATE INDEX idx_entity_closure_descendant ON entity_closure(descendant_id);

ALTER TABLE entity_closure ENABLE ROW LEVEL SECURITY;
ALTER TABLE entity_closure FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON entity_closure
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
