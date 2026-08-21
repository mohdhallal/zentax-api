-- Verify service_accounts
SELECT kind FROM users WHERE false;
SELECT id, token_hash, user_id, tenant_id, label, created_by, created_at, expires_at, last_used_at, revoked_at
FROM api_tokens
WHERE false;
