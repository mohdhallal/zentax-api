-- Revert tenants
BEGIN;

DROP TABLE IF EXISTS tenants;

COMMIT;
