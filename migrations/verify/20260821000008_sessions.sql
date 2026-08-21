-- Verify sessions
SELECT id, token_hash, user_id, tenant_id, mfa_pending, created_at, idle_expires_at,
       absolute_expires_at, revoked_at, ip, user_agent
FROM sessions
WHERE false;
