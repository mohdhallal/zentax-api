-- Verify invite_tokens
SELECT id, token_hash, user_id, tenant_id, created_by, created_at, expires_at, accepted_at, revoked_at
FROM invite_tokens
WHERE false;

-- The parent must be range-partitioned with a _default backstop.
SELECT 1 / (
    (SELECT relkind = 'p' FROM pg_class WHERE relname = 'invite_tokens')
    AND (SELECT to_regclass('invite_tokens_default') IS NOT NULL)
)::int;
