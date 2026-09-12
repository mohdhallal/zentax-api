-- Revert session_mfa_attempts
BEGIN;

ALTER TABLE sessions DROP COLUMN IF EXISTS mfa_attempts;

COMMIT;
