-- Revert audit_log
BEGIN;

DROP TABLE IF EXISTS audit_log;

COMMIT;
