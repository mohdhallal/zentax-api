-- Verify users
SELECT id, tenant_id, email, name, password_hash, status, totp_secret_enc, totp_enabled,
       failed_login_attempts, locked_until, created_at, updated_at
FROM users
WHERE false;
