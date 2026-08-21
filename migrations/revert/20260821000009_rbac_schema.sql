-- Revert rbac_schema
BEGIN;

DROP TABLE IF EXISTS entity_closure;
DROP TABLE IF EXISTS user_grants;

COMMIT;
