-- Verify imports
--
-- The three properties spreadsheet ingest rests on, proven on throwaway rows
-- inside a rolled-back transaction so verification leaves nothing behind:
--
--   1. a planned row cannot be edited after the customer has seen it;
--   2. a rejected batch can never become committed;
--   3. a committed batch is final.

BEGIN;

SELECT id, tenant_id, kind, file_name, byte_size, checksum, row_count, create_count,
       update_count, unchanged_count, invalid_count, status, created_by, created_at,
       committed_by, committed_at
  FROM import_batches WHERE FALSE;

SELECT tenant_id, batch_id, row_number, action, natural_key, target_id, target_updated_at,
       payload, changed_fields, issues
  FROM import_rows WHERE FALSE;

DO $$
DECLARE
    v_tenant   UUID;
    v_batch    UUID;
    v_rejected UUID;
    v_zeros    CHAR(64) := repeat('0', 64);
BEGIN
    SELECT id INTO v_tenant FROM tenants LIMIT 1;
    IF v_tenant IS NULL THEN
        RAISE NOTICE 'no tenant to verify against; table shape checked by definition only';
        RETURN;
    END IF;

    INSERT INTO import_batches
        (tenant_id, kind, file_name, byte_size, checksum, row_count,
         create_count, update_count, unchanged_count, invalid_count, status, created_by)
    VALUES (v_tenant, 'entities', 'verify.csv', 10, v_zeros, 1, 1, 0, 0, 0, 'validated', v_tenant)
    RETURNING id INTO v_batch;

    INSERT INTO import_rows (tenant_id, batch_id, row_number, action, natural_key, payload)
    VALUES (v_tenant, v_batch, 1, 'create', 'acme gmbh', '{"name": "Acme GmbH"}'::jsonb);

    -- A plan that promises a create must not be able to claim, afterwards, that
    -- it promised an update.
    BEGIN
        UPDATE import_rows SET action = 'update' WHERE tenant_id = v_tenant AND batch_id = v_batch;
        RAISE EXCEPTION 'a planned row was allowed to change';
    EXCEPTION WHEN sqlstate 'ZT036' THEN
        NULL; -- expected: import_row_immutable
    END;

    -- A file with a bad row is refused for good.
    INSERT INTO import_batches
        (tenant_id, kind, file_name, byte_size, checksum, row_count,
         create_count, update_count, unchanged_count, invalid_count, status, created_by)
    VALUES (v_tenant, 'entities', 'bad.csv', 10, v_zeros, 1, 0, 0, 0, 1, 'rejected', v_tenant)
    RETURNING id INTO v_rejected;

    BEGIN
        UPDATE import_batches SET status = 'committed', committed_at = NOW(), committed_by = v_tenant
         WHERE tenant_id = v_tenant AND id = v_rejected;
        RAISE EXCEPTION 'a rejected batch was allowed to commit';
    EXCEPTION WHEN sqlstate 'ZT036' THEN
        NULL; -- expected: import_batch_state_only
    END;

    -- And a committed one cannot restate what it promised.
    UPDATE import_batches SET status = 'committed', committed_at = NOW(), committed_by = v_tenant
     WHERE tenant_id = v_tenant AND id = v_batch;

    BEGIN
        UPDATE import_batches SET create_count = 99, row_count = 99
         WHERE tenant_id = v_tenant AND id = v_batch;
        RAISE EXCEPTION 'a committed batch was allowed to restate its plan';
    EXCEPTION WHEN sqlstate 'ZT036' THEN
        NULL; -- expected: import_batch_state_only
    END;
END $$;

ROLLBACK;
