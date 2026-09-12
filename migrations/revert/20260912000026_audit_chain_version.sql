-- Revert audit_chain_version
BEGIN;

-- Dropping the marker does not make pre-cut-over rows verifiable again; it only
-- takes away the verifier's ability to tell them apart from tampering (it then
-- reads every such row as a hash mismatch, which is the fail-safe direction).
ALTER TABLE audit_log DROP COLUMN IF EXISTS hash_version;

COMMIT;
