-- Verify internal_api_keys
SELECT id, app_name, key, secret_hash, active, created_at, updated_at
FROM internal_api_keys
WHERE false;
