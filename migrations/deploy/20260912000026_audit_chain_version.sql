-- Deploy audit_chain_version
BEGIN;

-- Hash-chain cut-over (ADR-0008). The chain's integrity claim is "recompute
-- the hash from the stored row"; until today the writer stamped occurred_at
-- with nanosecond precision and hashed it in that form, while this column is
-- TIMESTAMPTZ and keeps MICROseconds — so the last three digits were gone the
-- moment the row committed and NO entry written by a nanosecond-granular host
-- (every Linux container; macOS happens to be microsecond-granular) could ever
-- be recomputed. Fixed in platform/audit: the stamped instant is truncated to
-- audit.ChainResolution and the canonical form formats it at exactly that
-- resolution.
--
-- Rows written before the fix stay unverifiable forever — the hash is over an
-- instant Postgres never stored, and rewriting history to fix that is the one
-- thing an append-only ledger may not do. So each row now carries the hashing
-- contract it was written under, and the verifier reports what it could prove
-- instead of crying tamper:
--
--   1 = pre-cut-over: occurred_at hashed at nanosecond precision. Links
--       (prev_hash, seq) are still checked; the hash is not recomputable.
--   2 = occurred_at hashed at microsecond resolution = the stored value.
--
-- DEFAULT 1 is deliberate and stays: it back-fills every existing row as
-- unverifiable with no UPDATE (audit_log is append-only — there is no UPDATE
-- policy and the app role holds no UPDATE privilege), and it is the fail-safe
-- default for any writer that does not know about the column. The current
-- writer always sets the value explicitly.
--
-- platform/audit.Verify tolerates a contiguous v1 PREFIX and reports the first
-- verifiable seq; a v1 row appearing after a verifiable one is an error, so the
-- marker cannot be used to exempt a row in the middle of a live chain.
ALTER TABLE audit_log ADD COLUMN hash_version SMALLINT NOT NULL DEFAULT 1;

COMMENT ON COLUMN audit_log.hash_version IS
    'Hashing contract the row was written under: 1 = pre-cut-over (occurred_at hashed at nanosecond precision, not recomputable from this row), 2 = hashed at the column''s microsecond resolution. Written explicitly by platform/audit; DEFAULT 1 so an unaware writer is recorded as unverifiable.';

COMMIT;
