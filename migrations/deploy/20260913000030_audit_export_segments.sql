-- Deploy audit_export_segments
BEGIN;

-- The WORM export ledger (ADR-0008, integrity triad #3): one row per audit-chain
-- SEGMENT cut for a tenant and written to object storage.
--
-- What the table is FOR. The hash chain proves the trail was not edited, but it
-- proves it from rows a privileged operator could still drop, and a tenant that
-- has been deleted leaves no rows at all. The export writes each stretch of the
-- chain to an object store as a self-contained file, so the evidence outlives
-- the database. This table is the CURSOR and the RECEIPT for that: it says how
-- far a tenant's chain has been exported, and under which key each stretch was
-- written.
--
-- The three properties the job's correctness rests on are enforced HERE, not by
-- the job remembering:
--
--   at most one unfinished segment per tenant
--       uq_audit_export_segments_pending. A crash between "the range was cut"
--       and "the file landed" leaves exactly one pending row; the next pass
--       finds it and finishes THAT one instead of cutting a second range over
--       the same entries. A duplicate export is therefore impossible, and so is
--       a gap: nothing may be cut past a range that has not landed.
--
--   ranges are contiguous and non-overlapping
--       UNIQUE (tenant_id, from_seq) plus the range CHECK. Two passes that
--       somehow raced on the same starting point cannot both commit.
--
--   a landed segment is final
--       the state-only trigger below. Once status = 'uploaded' the row is
--       frozen: the range, the key, the digest and the chain endpoints it
--       attests to can never be rewritten to describe a different file.
--
-- Deliberately NOT partitioned — the ADR-0020 exception outbox_messages,
-- storage_reclaim and user_grants already take: a small, slow-growing ledger
-- (one row per segment per tenant) whose every query is a single-tenant index
-- lookup.
CREATE TABLE audit_export_segments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ON DELETE CASCADE, unlike audit_log's RESTRICT, and the difference is the
    -- point of the whole feature: this table is the RECEIPT, not the evidence.
    -- The evidence is in the object store, self-describing and verifiable
    -- without any database — which is exactly what a tenant that has erased its
    -- data leaves behind. The cursor for a tenant that no longer exists means
    -- nothing, so it goes with the tenant row, and a tenant delete (ADR-0007)
    -- does not have to know this table exists.
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,

    -- The segment's own ordinal in this tenant's series (1, 2, 3 …). A reader
    -- holding the files uses it to notice a missing one.
    segment_seq BIGINT NOT NULL,

    -- The stretch of audit_log.seq this segment carries, inclusive.
    from_seq BIGINT NOT NULL,
    to_seq   BIGINT NOT NULL,
    entries  INTEGER NOT NULL,

    -- The chain state at both ends: the prev_hash the first entry links back to
    -- (the previous segment's end_hash, or 64 zeros at genesis) and the hash of
    -- the last entry. Consecutive segments must meet exactly here, which is
    -- what makes a SERIES of files verifiable rather than just each file.
    start_prev_hash CHAR(64) NOT NULL,
    end_hash        CHAR(64) NOT NULL,

    -- Where it was written, in what shape, and what it hashes to. digest is the
    -- sha256 (hex) of the segment document inside the file — the same value the
    -- file carries, so a mismatch between the two is detectable without the
    -- database and a mismatch with a REBUILD from these rows is tamper evidence
    -- about the database.
    object_key     TEXT NOT NULL,
    format_version SMALLINT NOT NULL,
    digest         CHAR(64) NOT NULL,
    size_bytes     BIGINT NOT NULL,

    -- Stamped when the range is CUT, not when the file lands, and reused
    -- verbatim by a retry: it is inside the hashed document, so a rebuild after
    -- a crash has to produce the same bytes to produce the same digest.
    exported_at TIMESTAMPTZ NOT NULL,

    status VARCHAR(10) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'uploaded')),
    uploaded_at TIMESTAMPTZ,
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT audit_export_segments_range CHECK (
        from_seq >= 1 AND to_seq >= from_seq AND entries = to_seq - from_seq + 1
    ),
    CONSTRAINT audit_export_segments_state CHECK (
        (status = 'pending'  AND uploaded_at IS NULL)
     OR (status = 'uploaded' AND uploaded_at IS NOT NULL)
    ),
    CONSTRAINT audit_export_segments_attempts_sane CHECK (attempts >= 0),

    UNIQUE (tenant_id, segment_seq),
    UNIQUE (tenant_id, from_seq)
);

-- One unfinished segment per tenant, at most. This single index is what makes
-- "a re-run after a crash midway" safe.
CREATE UNIQUE INDEX uq_audit_export_segments_pending
    ON audit_export_segments (tenant_id) WHERE status = 'pending';

COMMENT ON TABLE audit_export_segments IS
    'WORM export ledger (ADR-0008): one row per audit-chain segment written to object storage; the cursor and the receipt.';
COMMENT ON COLUMN audit_export_segments.exported_at IS
    'Stamped when the range is cut and reused by a retry — it is inside the hashed document.';
COMMENT ON COLUMN audit_export_segments.digest IS
    'sha256 (hex) of the segment document inside the object; the object carries the same value.';

-- ── Row-level security ──────────────────────────────────────────────────────
--
-- Ordinary tenant isolation (ADR-0004) and nothing else. Unlike the outbox's
-- delivery loop, the export job needs NO cross-tenant door: it enumerates
-- tenants from the (RLS-free, control-plane) registry and then does each
-- tenant's work bound to that tenant, exactly as a request would be — which is
-- also why the audit entry it writes is accepted by audit_log's append-only
-- tenant policy.
ALTER TABLE audit_export_segments ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_export_segments FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON audit_export_segments
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Deliberately NO policy FOR DELETE: the receipt for evidence taken is itself
-- evidence. A retention policy that ever needs to trim it will do so as a
-- migration, deliberately, not as a side effect of application code.

-- ── What an update may move ─────────────────────────────────────────────────
--
-- A pending segment may be retried — the attempt counter, the error, and (when
-- the binary that retries writes a NEWER document format than the binary that
-- cut the range) the digest, size and format version of the rebuilt document.
-- Everything that identifies WHICH entries the segment attests to is frozen at
-- the cut, and an uploaded segment is frozen entirely.
--
-- SECURITY INVOKER (the default): a psql session and a superuser are bound by
-- it too.
CREATE FUNCTION audit_export_segment_state_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'uploaded' THEN
        RAISE EXCEPTION 'audit export segment % has landed and cannot be modified', OLD.id
            USING ERRCODE = 'ZT030',
                  HINT = 'a segment already written to the object store is final; cut a new one';
    END IF;

    IF NEW.tenant_id       IS DISTINCT FROM OLD.tenant_id
    OR NEW.segment_seq     IS DISTINCT FROM OLD.segment_seq
    OR NEW.from_seq        IS DISTINCT FROM OLD.from_seq
    OR NEW.to_seq          IS DISTINCT FROM OLD.to_seq
    OR NEW.entries         IS DISTINCT FROM OLD.entries
    OR NEW.start_prev_hash IS DISTINCT FROM OLD.start_prev_hash
    OR NEW.end_hash        IS DISTINCT FROM OLD.end_hash
    OR NEW.object_key      IS DISTINCT FROM OLD.object_key
    OR NEW.exported_at     IS DISTINCT FROM OLD.exported_at
    OR NEW.created_at      IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'audit export segment % may only change upload state', OLD.id
            USING ERRCODE = 'ZT030',
                  HINT = 'the range, the key and the chain endpoints are frozen when the range is cut';
    END IF;

    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION audit_export_segment_state_only() IS
    'Audit export: an update may move upload state only; the range/key/endpoints are frozen and a landed segment is final (SQLSTATE ZT030).';

CREATE TRIGGER audit_export_segment_state_only
    BEFORE UPDATE ON audit_export_segments
    FOR EACH ROW EXECUTE FUNCTION audit_export_segment_state_only();

COMMIT;
