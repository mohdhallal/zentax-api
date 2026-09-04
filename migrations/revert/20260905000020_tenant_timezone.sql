-- Revert tenant_timezone
BEGIN;

ALTER TABLE tenants DROP COLUMN IF EXISTS timezone;

COMMIT;
