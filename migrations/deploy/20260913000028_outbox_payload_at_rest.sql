-- Deploy outbox_payload_at_rest
BEGIN;

-- The outbox payload is a SEALED envelope, and settlement CLEARS it.
--
-- What went wrong. 20260913000027 described payload as "the frozen variable set
-- for that render" and recipient as "the only personal datum on the row". Both
-- are now false in a way that matters: the member.invited payload carries the
-- cleartext "zti_…" invite token — the very value invite_tokens refuses to
-- store, keeping only a SHA-256 so that a leaked table cannot be replayed — plus
-- the invitee's display name, and the deadline.reminder payload carries a
-- tenant's entity names, task names and filing dates. Nothing bounded either
-- one but the generic 30-day pruner, so a database backup taken any time in a
-- month yielded working activation links for somebody else's tenant.
--
-- Two changes, one here and one in the application:
--
--   1. platform/outbox seals the payload with AES-256-GCM under the
--      application's encryption key (the same key that protects
--      users.totp_secret_enc, ADR-0006) before the INSERT, and opens it in the
--      delivery loop just before the transport call. The column stays JSONB and
--      holds {"encV":1,"enc":"<base64 nonce||ciphertext>"}, so no type, index or
--      policy changes and rows queued before the cut-over still deliver.
--
--   2. This migration lets a SETTLEMENT clear the payload, which the state-only
--      guard previously forbade — it refused ANY update that changed payload,
--      so the content could only ever be removed by deleting the whole row.
--      Clearing at settlement is what turns the exposure window from "until the
--      pruner gets to it" into "until the mail goes out".
--
-- The guard stays otherwise exactly as strict. The payload may change in ONE
-- direction only — to '{}' — and only on the statement that settles the row;
-- re-addressing a message, editing its content, and resurrecting a settled one
-- are all still refused with SQLSTATE ZT027. So a redelivery is still, by
-- construction, the identical mail: while a message is pending its content
-- cannot be touched, and once it is settled there is no redelivery at all.
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

COMMENT ON FUNCTION outbox_message_state_only() IS
    'Outbox: an update may move delivery state only, and may clear the payload when it settles the row; addressing is frozen and a settled message is final (SQLSTATE ZT027).';

-- The rows that are already here.
--
-- A message settled BEFORE this change kept its cleartext payload, and it would
-- otherwise keep it until the 30-day pruner arrived — which is precisely the
-- exposure being closed, so it is closed for the existing rows too rather than
-- only for the next ones. The trigger is lifted for this one statement because
-- a settled row is immutable by design and this is the migration that is
-- allowed to say otherwise; the lock is brief and the table is a drained queue.
--
-- PENDING rows are deliberately untouched: they are still owed, they are still
-- renderable (Seal.Open passes a cleartext payload through), and each one seals
-- nothing but shortens its own life by being delivered.
ALTER TABLE outbox_messages DISABLE TRIGGER outbox_message_state_only;
UPDATE outbox_messages
   SET payload = '{}'::jsonb, updated_at = updated_at
 WHERE status <> 'pending' AND payload <> '{}'::jsonb;
ALTER TABLE outbox_messages ENABLE TRIGGER outbox_message_state_only;

COMMENT ON COLUMN outbox_messages.payload IS
    'Sealed variable set: {"encV":1,"enc":"<base64 nonce||ciphertext>"} under the application encryption key (ADR-0006). Holds a live invite credential for member.invited, so it is ciphertext at rest and is cleared to ''{}'' the moment the message settles.';

COMMENT ON COLUMN outbox_messages.recipient IS
    'The recipient address: the only personal datum the row keeps IN THE CLEAR (the payload holds more, sealed). Erased by the prune job, never written to the audit trail.';

COMMIT;
