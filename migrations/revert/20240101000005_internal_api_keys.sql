-- Revert internal_api_keys
BEGIN;

DROP TABLE IF EXISTS internal_api_keys;

COMMIT;
