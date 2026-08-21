-- Revert service_accounts
BEGIN;

DROP TABLE IF EXISTS api_tokens;
ALTER TABLE users DROP COLUMN IF EXISTS kind;

COMMIT;
