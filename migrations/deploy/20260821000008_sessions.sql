-- Deploy sessions
BEGIN;

-- Server-side sessions (ADR-0011). The opaque cookie token is stored only as its
-- SHA-256 hash, so a leaked table cannot forge cookies. NOT RLS-scoped: looked up
-- by token_hash before any tenant context exists. Postgres store for now; Redis
-- is Phase 2. mfa_pending sessions are rejected everywhere except MFA verify.
-- Partitioned by RANGE(created_at), monthly (ADR-0020): sessions are a churn
-- stream with a bounded lifetime, so pruning expired rows becomes a partition
-- drop (older than the absolute session lifetime) instead of DELETE storms.
-- Unique keys must include the partition key, so token_hash uniqueness is
-- per-partition — fine: the token is 256 bits of entropy hashed with SHA-256,
-- a cross-partition collision is not a real event; lookups scan the (few,
-- monthly) partition indexes. The _default partition is a backstop for a
-- lapsed create-ahead; the migrate job tops partitions up on every run.
CREATE TABLE sessions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mfa_pending BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    ip TEXT,
    user_agent TEXT,
    PRIMARY KEY (id, created_at),
    UNIQUE (token_hash, created_at)
) PARTITION BY RANGE (created_at);

SELECT ensure_month_partitions('sessions', DATE '2026-08-01', 3);
CREATE TABLE sessions_default PARTITION OF sessions DEFAULT;
ALTER TABLE sessions_default ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions_default FORCE ROW LEVEL SECURITY;

CREATE INDEX idx_sessions_user_id ON sessions(user_id);

COMMIT;
