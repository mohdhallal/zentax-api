-- Deploy service_accounts
BEGIN;

-- Machine identity (agentic-AI readiness B1; ADR-0011/0012). A service account
-- IS a user row (kind='service'), so grants, audit actor_id, and
-- created_by/updated_by attribution all reuse the same rails. Service accounts
-- get a synthetic unique @service.zentax.internal email (never a login: they
-- have no password, and the login path rejects kind='service' outright).
ALTER TABLE users
    ADD COLUMN kind VARCHAR(10) NOT NULL DEFAULT 'human'
        CHECK (kind IN ('human', 'service'));

-- API tokens: the bearer credential for service accounts. Stored ONLY as a
-- SHA-256 hash (like sessions); looked up by hash before any tenant context
-- exists, hence NOT RLS-scoped — every tenant-sensitive query in the app layer
-- scopes by tenant_id explicitly. Expiry is mandatory; revocation is a
-- tombstone (revoked_at), never a delete, so issuance history survives.
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL, -- the admin who issued it
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_tenant_id ON api_tokens(tenant_id);

COMMIT;
