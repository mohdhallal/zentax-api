-- Deploy audit_request_id_shape
BEGIN;

-- The one door by which CALLER TEXT could reach this table.
--
-- request_id is TEXT NOT NULL and, until the middleware was closed today,
-- whatever arrived in the X-Request-Id header was written into it verbatim,
-- folded into the per-tenant hash chain, and copied into WORM segments that a
-- COMPLIANCE-mode Object Lock keeps for ten years — a mode with no bypass for
-- any principal, under a bucket policy that denies every delete verb. So an
-- authenticated caller (or, in the self-host edition, anyone who can reach the
-- API port, which docker-compose publishes) could write arbitrary bytes into an
-- append-only store from which nothing can ever be removed. That contradicts
-- ADR-0008's own rule — the envelope carries no free text — and it is precisely
-- the door security_events closed on purpose by typing ITS request_id as UUID.
--
-- Two halves, because one is not enough:
--   * the middleware honours the header only when it is a canonical UUID and
--     mints one otherwise (delivery/httpkit/middlewares/request_id.go), and
--     platform/audit re-checks the shape for the paths that have no middleware;
--   * this CHECK, which no future code path can bypass — the same way the
--     64-hex CHECK on security_events.subject_digest makes "never in the clear"
--     structural rather than a promise.
--
-- Lowercase only: Postgres renders the sibling stream's UUID column lowercase,
-- so a mixed-case spelling would be an id that joins to nothing. The middleware
-- lowercases what a caller sends rather than refusing it.
--
-- NOT VALID, and this is the honest part: rows already written cannot be fixed.
-- audit_log is append-only (no UPDATE policy, no UPDATE privilege) and every
-- value is inside a hash, so rewriting one would break the chain it belongs to.
-- The constraint therefore governs every FUTURE row — including rows in
-- partitions created later, and in the default partition — and leaves history
-- as it is. The NOTICE below says how much history is affected, which on a
-- deployment whose callers never sent the header is none.
ALTER TABLE audit_log ADD CONSTRAINT audit_log_request_id_shape
    CHECK (request_id = ''
        OR request_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
    NOT VALID;

DO $$
DECLARE
    offending BIGINT;
BEGIN
    SELECT count(*) INTO offending
    FROM audit_log
    WHERE request_id <> ''
      AND request_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

    IF offending = 0 THEN
        RAISE NOTICE 'audit_log.request_id: no stored row carries caller text; the constraint holds for history as well as for the future';
    ELSE
        RAISE NOTICE 'audit_log.request_id: % stored row(s) carry a request id of another shape. They are hash-covered in an append-only table and CANNOT be corrected; they are left as they are, and the constraint governs every future row. Review them before the next WORM export: SELECT tenant_id, seq, request_id FROM audit_log WHERE request_id <> '''' AND request_id !~ ''^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'';', offending;
    END IF;
END
$$;

COMMENT ON COLUMN audit_log.request_id IS
    'Correlation id for the request that caused the entry: a canonical lowercase UUID, or the empty string when there was no request (scheduled work). Caller-supplied via X-Request-Id and therefore SHAPE-CHECKED at the door and again here — this column is hashed into the chain and exported to a WORM bucket, so it must never become a place a caller can write text. Joins security_events.request_id.';

COMMIT;
