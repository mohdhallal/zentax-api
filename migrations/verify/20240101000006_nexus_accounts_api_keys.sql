-- Verify nexus_accounts_api_keys
SELECT id, nexus_account_id, api_key, api_secret, created_at, updated_at
FROM nexus_accounts_api_keys
WHERE false;
