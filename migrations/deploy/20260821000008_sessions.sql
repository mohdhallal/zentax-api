-- Deploy sessions
BEGIN;

-- Server-side sessions (ADR-0011). The opaque cookie token is stored only as its
-- SHA-256 hash, so a leaked table cannot forge cookies. NOT RLS-scoped: looked up
-- by token_hash before any tenant context exists. Postgres store for now; Redis
-- is Phase 2. mfa_pending sessions are rejected everywhere except MFA verify.
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mfa_pending BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    ip TEXT,
    user_agent TEXT
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);

COMMIT;
