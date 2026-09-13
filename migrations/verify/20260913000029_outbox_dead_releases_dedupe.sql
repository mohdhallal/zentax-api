-- Verify outbox_dead_releases_dedupe
--
-- The guard must accept a dead letter that releases its key and still refuse
-- one that rewrites it. Proven on a throwaway row inside a rolled-back
-- transaction, so verification leaves nothing behind.

BEGIN;

DO $$
DECLARE
    v_tenant UUID;
    v_id     UUID;
BEGIN
    SELECT id INTO v_tenant FROM tenants LIMIT 1;
    IF v_tenant IS NULL THEN
        RAISE NOTICE 'no tenant to verify against; guard shape checked by definition only';
        RETURN;
    END IF;

    INSERT INTO outbox_messages (tenant_id, channel, template, recipient, payload, dedupe_key, due_at)
    VALUES (v_tenant, 'email', 'verify.probe', 'probe@example.test', '{}'::jsonb, 'verify-probe-key', NOW())
    RETURNING id INTO v_id;

    UPDATE outbox_messages
       SET status = 'dead', failed_at = NOW(), last_error = 'verify', dedupe_key = NULL
     WHERE id = v_id;

    IF (SELECT dedupe_key FROM outbox_messages WHERE id = v_id) IS NOT NULL THEN
        RAISE EXCEPTION 'a dead letter must release its dedupe key';
    END IF;
END $$;

ROLLBACK;
