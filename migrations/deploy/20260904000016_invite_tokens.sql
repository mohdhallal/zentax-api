-- Deploy invite_tokens
BEGIN;

-- Invite tokens: the one-time credential an invited human (users.status =
-- 'invited', no password yet) redeems to set a password and become active.
-- Stored ONLY as a SHA-256 hash (cleartext shown once at issuance, like
-- api_tokens / sessions). Expiry is mandatory (7 days, set by the app);
-- acceptance and revocation are tombstones (accepted_at / revoked_at), never
-- deletes, so issuance history survives.
--
-- NOT RLS-scoped: the accept flow looks a token up by hash BEFORE any tenant
-- context / session exists (exactly like api_tokens and sessions); the tenant
-- is carried on the row and the app binds app.tenant_id from it for the
-- activation transaction. Every tenant-sensitive statement in the app layer
-- scopes by tenant_id / user_id explicitly.
--
-- Partitioned by RANGE(created_at), monthly (ADR-0020): an expiring stream —
-- old partitions can be dropped once past the invite lifetime. token_hash
-- uniqueness is per-partition (partition-key rule) — fine, the hash is over
-- 256 bits of entropy. The _default partition is the backstop for a lapsed
-- create-ahead; migrate.sh tops partitions up on every run.
CREATE TABLE invite_tokens (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL, -- the admin who issued it
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (id, created_at),
    UNIQUE (token_hash, created_at)
) PARTITION BY RANGE (created_at);

SELECT ensure_month_partitions('invite_tokens', DATE '2026-09-01', 3);
CREATE TABLE invite_tokens_default PARTITION OF invite_tokens DEFAULT;
ALTER TABLE invite_tokens_default ENABLE ROW LEVEL SECURITY;
ALTER TABLE invite_tokens_default FORCE ROW LEVEL SECURITY;

CREATE INDEX idx_invite_tokens_user_id ON invite_tokens(user_id);
CREATE INDEX idx_invite_tokens_tenant_id ON invite_tokens(tenant_id);

COMMIT;
