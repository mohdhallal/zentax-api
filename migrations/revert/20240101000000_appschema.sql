-- Revert appschema
BEGIN;

DROP EXTENSION IF EXISTS "pgcrypto";

COMMIT;
