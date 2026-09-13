-- Verify audit_export_segments
--
-- The two invariants the export job's crash-safety rests on: only ONE pending
-- segment may exist per tenant, and a landed segment is final. Proven on
-- throwaway rows inside a rolled-back transaction, so verification leaves
-- nothing behind.

BEGIN;

SELECT id, tenant_id, segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
       object_key, format_version, digest, size_bytes, exported_at, status, uploaded_at,
       attempts, last_error, created_at, updated_at
  FROM audit_export_segments
 WHERE FALSE;

-- The receipt follows its tenant. audit_log is ON DELETE RESTRICT, on purpose;
-- this table must NOT be, or every tenant delete (ADR-0007 erasure, and
-- seed-demo reset) would fail on a table it has no reason to know about. The
-- evidence itself is in the object store and is unaffected either way.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'audit_export_segments'::regclass
           AND contype = 'f' AND confdeltype = 'c'
    ) THEN
        RAISE EXCEPTION 'audit_export_segments.tenant_id must be ON DELETE CASCADE';
    END IF;
END $$;

DO $$
DECLARE
    v_tenant UUID;
    v_first  UUID;
    v_zeros  CHAR(64) := repeat('0', 64);
BEGIN
    SELECT id INTO v_tenant FROM tenants LIMIT 1;
    IF v_tenant IS NULL THEN
        RAISE NOTICE 'no tenant to verify against; table shape checked by definition only';
        RETURN;
    END IF;

    INSERT INTO audit_export_segments
        (tenant_id, segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
         object_key, format_version, digest, size_bytes, exported_at)
    VALUES (v_tenant, 1, 1, 2, 2, v_zeros, v_zeros, 'verify/probe-1.json', 1, v_zeros, 10, NOW())
    RETURNING id INTO v_first;

    BEGIN
        INSERT INTO audit_export_segments
            (tenant_id, segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
             object_key, format_version, digest, size_bytes, exported_at)
        VALUES (v_tenant, 2, 3, 4, 2, v_zeros, v_zeros, 'verify/probe-2.json', 1, v_zeros, 10, NOW());
        RAISE EXCEPTION 'a second PENDING segment was accepted for the same tenant';
    EXCEPTION WHEN unique_violation THEN
        NULL; -- expected: uq_audit_export_segments_pending
    END;

    UPDATE audit_export_segments SET status = 'uploaded', uploaded_at = NOW() WHERE id = v_first;

    BEGIN
        UPDATE audit_export_segments SET to_seq = 99, entries = 99 WHERE id = v_first;
        RAISE EXCEPTION 'a landed segment was allowed to change its range';
    EXCEPTION WHEN sqlstate 'ZT030' THEN
        NULL; -- expected: the state-only trigger
    END;
END $$;

ROLLBACK;
