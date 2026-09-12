-- Deploy session_mfa_attempts
BEGIN;

-- THE SECOND-FACTOR ATTEMPT BUDGET — the MFA analogue of
-- users.failed_login_attempts, and the column that makes the second factor a
-- factor at all. Until it existed, POST /auth/mfa/verify counted nothing and
-- invalidated nothing on a wrong code, so the six-digit code in front of a
-- pending session could be retried without limit: a script walked the whole
-- million-value space against a session that never went away. The policy that
-- reads and writes this column is stated once, in
-- modules/identity/usecases/mfa.go.
--
-- WHY ON sessions AND NOT ON users. The budget is per PENDING SESSION, not per
-- account: the thing being guessed is the code that completes ONE login, and
-- spending the budget destroys that session (revoked_at) rather than locking the
-- account — the user simply signs in again. Putting it on users would let anyone
-- holding a stolen password lock a victim out of their own account by burning
-- MFA codes, and would conflate two controls with different resets.
--
-- SMALLINT, NOT NULL DEFAULT 0, saturating at the threshold in the UPDATE that
-- charges it (past the threshold the number means nothing). ADD COLUMN with a
-- constant default takes no table rewrite (PG 11+) and recurses to every
-- partition of this RANGE(created_at)-partitioned table, so it is safe to apply
-- online.
ALTER TABLE sessions
    ADD COLUMN mfa_attempts SMALLINT NOT NULL DEFAULT 0;

COMMIT;
