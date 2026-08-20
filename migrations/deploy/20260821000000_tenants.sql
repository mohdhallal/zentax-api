-- Deploy tenants
BEGIN;

-- The tenant registry. This is control-plane metadata (ADR-0005): it is NOT
-- itself tenant-scoped and carries no RLS — every tenant-scoped table
-- references it via tenant_id.
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(63) NOT NULL UNIQUE,
    name TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants(slug);

COMMIT;
