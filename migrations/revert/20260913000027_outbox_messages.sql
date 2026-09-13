-- Revert outbox_messages
BEGIN;

-- Dropping the table takes its trigger, indexes and policies with it; the
-- guard function is standalone and has to go by name.
DROP TABLE IF EXISTS outbox_messages;
DROP FUNCTION IF EXISTS outbox_message_state_only();

COMMIT;
