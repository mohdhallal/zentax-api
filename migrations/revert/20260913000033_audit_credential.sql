-- Revert audit_credential
BEGIN;

-- Dropping the columns loses the join to the authentication stream for every
-- entry that carried one, and it does NOT make those entries verifiable again:
-- their hashes were computed under hash_version 3, which covers the credential,
-- so a build that has reverted this change reads them as tampering. Revert only
-- together with the code that writes version 3.
DROP INDEX IF EXISTS idx_audit_log_credential;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_credential_kind;
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_credential_pair;

ALTER TABLE audit_log DROP COLUMN IF EXISTS credential_kind;
ALTER TABLE audit_log DROP COLUMN IF EXISTS credential_id;

COMMENT ON COLUMN audit_log.hash_version IS
    'Hashing contract the row was written under: 1 = pre-cut-over (occurred_at hashed at nanosecond precision, not recomputable from this row), 2 = hashed at the column''s microsecond resolution. Written explicitly by platform/audit; DEFAULT 1 so an unaware writer is recorded as unverifiable.';

COMMIT;
