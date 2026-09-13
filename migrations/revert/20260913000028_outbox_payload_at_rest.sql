-- Revert outbox_payload_at_rest
BEGIN;

-- Back to the guard as 20260913000027 wrote it: an update may move delivery
-- state and NOTHING else, payload included.
--
-- Reverting this is only safe with the application reverted too. A build that
-- clears the payload on settlement cannot settle anything once this trigger is
-- back — every MarkSent and every dead-letter raises ZT027, the message is
-- never written down, and the queue redelivers it on the next lease. Revert the
-- code first, then this.
CREATE OR REPLACE FUNCTION outbox_message_state_only() RETURNS trigger
LANGUAGE plpgsql AS $$
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
    OR NEW.payload    IS DISTINCT FROM OLD.payload
    OR NEW.dedupe_key IS DISTINCT FROM OLD.dedupe_key
    OR NEW.due_at     IS DISTINCT FROM OLD.due_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at
    OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'outbox message % may only change delivery state', OLD.id
            USING ERRCODE = 'ZT027',
                  HINT = 'addressing and content are frozen at enqueue; a redelivery must be identical';
    END IF;

    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION outbox_message_state_only() IS
    'Outbox: an update may move delivery state only; addressing/content are frozen and a settled message is final (SQLSTATE ZT027).';

COMMENT ON COLUMN outbox_messages.payload IS NULL;
COMMENT ON COLUMN outbox_messages.recipient IS NULL;

COMMIT;
