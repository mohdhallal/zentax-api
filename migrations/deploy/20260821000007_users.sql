-- Deploy users
BEGIN;

-- Users are the app-owned identity records (ADR-0011). NOT RLS-scoped: login
-- must look a user up by email before any tenant context / GUC exists. email is
-- globally unique for v1 (stored lowercased by the app); ADR-0005 domain-scoped
-- identity refines this later. password_hash is argon2id (nullable for future
-- SSO-only users); totp_secret_enc is AES-GCM-encrypted (interim, ADR-0006).
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    password_hash TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'active', -- active | invited | disabled
    totp_secret_enc TEXT,
    totp_enabled BOOLEAN NOT NULL DEFAULT false,
    failed_login_attempts SMALLINT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_tenant_id ON users(tenant_id);

COMMIT;
