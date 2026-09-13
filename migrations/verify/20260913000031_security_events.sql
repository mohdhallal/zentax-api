-- Verify security_events
SELECT event_id, occurred_at, event, outcome, method, reason,
       tenant_id, principal_id, actor_id, session_id,
       subject_digest, client_ip, request_id
FROM security_events
WHERE false;

-- Partitioned by RANGE(occurred_at), with the _default backstop present.
SELECT 1 / (
    (SELECT c.relkind = 'p' FROM pg_class c WHERE c.relname = 'security_events')
    AND (SELECT pg_get_partkeydef(c.oid) = 'RANGE (occurred_at)'
           FROM pg_class c WHERE c.relname = 'security_events')
    AND EXISTS (SELECT 1 FROM pg_class WHERE relname = 'security_events_default')
)::int;

-- BOTH correlators optional — the point of the whole table. A stream that
-- required a tenant or an actor could not hold a failed login for an address
-- nobody recognises, which is the event a breach investigation starts from.
SELECT 1 / (
    (SELECT NOT attnotnull FROM pg_attribute
      WHERE attrelid = 'security_events'::regclass AND attname = 'tenant_id')
    AND (SELECT NOT attnotnull FROM pg_attribute
          WHERE attrelid = 'security_events'::regclass AND attname = 'principal_id')
    AND (SELECT NOT attnotnull FROM pg_attribute
          WHERE attrelid = 'security_events'::regclass AND attname = 'subject_digest')
)::int;

-- The submitted address can never be stored in the clear: the digest column is
-- constrained to 64 lowercase hex characters, and the two correlators are
-- mutually exclusive.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_constraint
             WHERE conrelid = 'security_events'::regclass
               AND contype = 'c'
               AND pg_get_constraintdef(oid) LIKE '%[0-9a-f]{64}%')
    AND EXISTS (SELECT 1 FROM pg_constraint
                 WHERE conrelid = 'security_events'::regclass
                   AND conname = 'security_events_one_correlator')
)::int;

-- No column can hold caller text: everything is a code constant, a UUID, an
-- INET, a timestamp or the hex digest. In particular request_id is a UUID (it is
-- caller-supplied when X-Request-Id is sent) and client_ip is INET.
SELECT 1 / (
    (SELECT format_type(atttypid, atttypmod) = 'uuid' FROM pg_attribute
      WHERE attrelid = 'security_events'::regclass AND attname = 'request_id')
    AND (SELECT format_type(atttypid, atttypmod) = 'inet' FROM pg_attribute
          WHERE attrelid = 'security_events'::regclass AND attname = 'client_ip')
    AND NOT EXISTS (SELECT 1 FROM pg_attribute
                     WHERE attrelid = 'security_events'::regclass
                       AND attnum > 0 AND NOT attisdropped
                       AND format_type(atttypid, atttypmod) IN ('text', 'jsonb', 'json'))
)::int;

-- RLS enabled AND forced; append-only (INSERT + SELECT policies only); the
-- unconditional append door (the writer has no tenant yet), the tenant's own
-- read, and the narrow cross-tenant GUC door.
SELECT 1 / (
    (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'security_events')
    AND EXISTS (SELECT 1 FROM pg_policies
                 WHERE schemaname = current_schema() AND tablename = 'security_events'
                   AND policyname = 'append' AND cmd = 'INSERT' AND with_check = 'true')
    AND EXISTS (SELECT 1 FROM pg_policies
                 WHERE schemaname = current_schema() AND tablename = 'security_events'
                   AND policyname = 'tenant_read' AND cmd = 'SELECT'
                   AND qual LIKE '%app.tenant_id%')
    AND EXISTS (SELECT 1 FROM pg_policies
                 WHERE schemaname = current_schema() AND tablename = 'security_events'
                   AND policyname = 'stream_read' AND cmd = 'SELECT'
                   AND qual LIKE '%app.security_stream%')
    AND NOT EXISTS (SELECT 1 FROM pg_policies
                     WHERE schemaname = current_schema() AND tablename = 'security_events'
                       AND cmd IN ('UPDATE', 'DELETE', 'ALL'))
)::int;

-- The second belt: a row trigger that refuses UPDATE and DELETE even for a role
-- that bypasses RLS (migrate.sh re-grants UPDATE/DELETE on ALL TABLES every run,
-- so a REVOKE in the migration would not survive a deploy — a trigger does).
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_proc p
             JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE p.proname = 'security_event_append_only' AND n.nspname = current_schema())
    AND EXISTS (SELECT 1 FROM pg_trigger t
                 JOIN pg_class c ON c.oid = t.tgrelid
                 WHERE t.tgname = 'security_event_append_only'
                   AND NOT t.tgisinternal
                   AND c.relname = 'security_events'
                   AND (t.tgtype & 1) = 1      -- FOR EACH ROW
                   AND (t.tgtype & 2) = 2      -- BEFORE
                   AND (t.tgtype & 8) = 8      -- DELETE
                   AND (t.tgtype & 16) = 16)   -- UPDATE
)::int;

-- The four investigative indexes.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema()
             AND indexname = 'idx_security_events_principal')
    AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema()
                 AND indexname = 'idx_security_events_subject')
    AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema()
                 AND indexname = 'idx_security_events_client_ip')
    AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema()
                 AND indexname = 'idx_security_events_tenant')
)::int;
