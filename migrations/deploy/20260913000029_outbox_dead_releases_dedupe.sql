-- Deploy outbox_dead_releases_dedupe

BEGIN;

-- A dead letter must not silence tomorrow's attempt at the same thing.
--
-- The producer's idempotency key is what stops a second copy of a message the
-- product already queued: the daily digest keys on (tenant, recipient, civil
-- day), so a sweep that runs twice enqueues once. The unique index enforces it
-- across every row, settled or not, which is right for a DELIVERED message —
-- the person has it, do not send it again — and wrong for a DEAD one.
--
-- Measured: a provider outage that outlasts the attempt budget dead-letters the
-- digest, the row keeps its key, and every later sweep of that civil day is
-- refused as a duplicate. The tenant's digest for that day is then lost for
-- good, silently, and the only trace is a `dead` row nobody reads. That is the
-- opposite of what the key is for: a message nobody received is exactly the one
-- worth queueing again.
--
-- So settling a message DEAD releases its key. Delivery keeps it, because a
-- delivered message must never be duplicated. The guard is relaxed by one
-- clause, in one direction only: the key may go to NULL, and only on the
-- statement that moves the row to 'dead'.
CREATE OR REPLACE FUNCTION outbox_message_state_only() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload_cleared BOOLEAN := NEW.status <> 'pending' AND NEW.payload = '{}'::jsonb;
    dedupe_released BOOLEAN := NEW.status = 'dead' AND NEW.dedupe_key IS NULL;
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
    OR (NEW.payload    IS DISTINCT FROM OLD.payload    AND NOT payload_cleared)
    OR (NEW.dedupe_key IS DISTINCT FROM OLD.dedupe_key AND NOT dedupe_released)
    OR NEW.due_at     IS DISTINCT FROM OLD.due_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at
    OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'outbox message % may only change delivery state', OLD.id
            USING ERRCODE = 'ZT027',
                  HINT = 'addressing and content are frozen at enqueue; settling may clear the payload, and a dead letter may release the dedupe key';
    END IF;

    RETURN NEW;
END;
$$;

-- Rows already dead keep a key nobody can reuse: release theirs too, so a
-- deployment that lost a digest to an outage before this migration can send
-- the next one.
UPDATE outbox_messages SET dedupe_key = NULL
WHERE status = 'dead' AND dedupe_key IS NOT NULL;

COMMIT;
