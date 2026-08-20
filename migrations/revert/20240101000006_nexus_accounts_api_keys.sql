-- Revert nexus_accounts_api_keys
BEGIN;

DROP TABLE IF EXISTS nexus_accounts_api_keys;

COMMIT;
