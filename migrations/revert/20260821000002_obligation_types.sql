-- Revert obligation_types
BEGIN;

DROP TABLE IF EXISTS obligation_types;

COMMIT;
