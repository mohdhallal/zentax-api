-- Verify outbox_payload_at_rest

-- The guard knows about clearing: the exemption is in the function body, and
-- the trigger it backs is still the same BEFORE UPDATE FOR EACH ROW.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_proc p
            JOIN pg_namespace n ON n.oid = p.pronamespace
            WHERE p.proname = 'outbox_message_state_only'
              AND n.nspname = current_schema()
              AND p.prosrc LIKE '%payload_cleared%')
    AND EXISTS (SELECT 1 FROM pg_trigger t
                JOIN pg_class c ON c.oid = t.tgrelid
                WHERE t.tgname = 'outbox_message_state_only'
                  AND NOT t.tgisinternal
                  AND c.relname = 'outbox_messages'
                  AND (t.tgtype & 1) = 1     -- FOR EACH ROW
                  AND (t.tgtype & 2) = 2     -- BEFORE
                  AND (t.tgtype & 16) = 16)  -- UPDATE
)::int;

-- The column says what it holds. An operator reading the schema must be able to
-- learn that this column is sealed, that it carries a credential, and that it
-- is cleared at settlement — the disclosure half of the fix.
SELECT 1 / (
    (SELECT col_description('outbox_messages'::regclass, attnum) LIKE '%encV%'
       FROM pg_attribute WHERE attrelid = 'outbox_messages'::regclass AND attname = 'payload')
    AND (SELECT col_description('outbox_messages'::regclass, attnum) LIKE '%cleared%'
           FROM pg_attribute WHERE attrelid = 'outbox_messages'::regclass AND attname = 'payload')
    AND (SELECT col_description('outbox_messages'::regclass, attnum) IS NOT NULL
           FROM pg_attribute WHERE attrelid = 'outbox_messages'::regclass AND attname = 'recipient')
)::int;
