-- Revert outbox_dead_releases_dedupe
--
-- Restores the guard of migration 28 exactly: a dead letter then keeps its
-- idempotency key again, and the day's message cannot be re-queued. Keys
-- already released stay NULL — a re-queue after this revert is refused by the
-- unique index only if a live row still holds the key, which is the pre-28
-- behaviour.

BEGIN;

CREATE OR REPLACE FUNCTION outbox_message_state_only() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload_cleared BOOLEAN := NEW.status <> 'pending' AND NEW.payload = '{}'::jsonb;
BEGIN
    IF OLD.status <> 'pending' THEN
        RAISE EXCEPTION 'outbox message % is settled (%) and cannot be modified', OLD.id, OLD.status
            USING ERRCODE = 'ZT027',
                  HINT = 'queue a new message instead of resurrecting a settled one';
    END IF;

    IF NEW.tenant_id  IS DISTINCT FROM OLD.tenant_id
    OR NEW.channel    IS DISTINCT FROM OLD.channel
    OR NEW.template   IS DISTINCT FROM OLD.template
    OR NEW.recipient  IS DISTINCT FROM OLD.recipient
    OR (NEW.payload   IS DISTINCT FROM OLD.payload AND NOT payload_cleared)
    OR NEW.dedupe_key IS DISTINCT FROM OLD.dedupe_key
    OR NEW.due_at     IS DISTINCT FROM OLD.due_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at
    OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'outbox message % may only change delivery state', OLD.id
            USING ERRCODE = 'ZT027',
                  HINT = 'addressing and content are frozen at enqueue; settling may only CLEAR the payload';
    END IF;

    RETURN NEW;
END;
$$;

COMMIT;
