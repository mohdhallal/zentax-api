-- Verify outbox_messages
SELECT id, tenant_id, channel, template, payload, recipient, dedupe_key,
       due_at, next_attempt_at, attempts, status, last_error, sent_at, failed_at,
       created_at, updated_at, created_by
FROM outbox_messages
WHERE false;

-- RLS enabled AND forced; the tenant policy present; the runner's door present
-- and NARROW — read + settle, no INSERT policy (the runner can never author a
-- message) and a DELETE policy that only reaches settled rows.
SELECT 1 / (
    (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'outbox_messages')
    AND EXISTS (SELECT 1 FROM pg_policies
                WHERE schemaname = current_schema() AND tablename = 'outbox_messages'
                  AND policyname = 'tenant_isolation' AND cmd = 'ALL')
    AND EXISTS (SELECT 1 FROM pg_policies
                WHERE schemaname = current_schema() AND tablename = 'outbox_messages'
                  AND policyname = 'runner_read' AND cmd = 'SELECT'
                  AND qual LIKE '%app.outbox_runner%')
    AND EXISTS (SELECT 1 FROM pg_policies
                WHERE schemaname = current_schema() AND tablename = 'outbox_messages'
                  AND policyname = 'runner_settle' AND cmd = 'UPDATE'
                  AND qual LIKE '%app.outbox_runner%' AND with_check LIKE '%app.outbox_runner%')
    AND EXISTS (SELECT 1 FROM pg_policies
                WHERE schemaname = current_schema() AND tablename = 'outbox_messages'
                  AND policyname = 'runner_prune' AND cmd = 'DELETE'
                  AND qual LIKE '%status%')
    AND NOT EXISTS (SELECT 1 FROM pg_policies
                    WHERE schemaname = current_schema() AND tablename = 'outbox_messages'
                      AND cmd = 'INSERT')
)::int;

-- The claim index (partial, on the pending schedule), the prune index, and the
-- per-tenant idempotency key.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_indexes
            WHERE schemaname = current_schema() AND indexname = 'idx_outbox_messages_due'
              AND indexdef LIKE '%(next_attempt_at, id)%'
              AND indexdef LIKE '%WHERE ((status)::text = ''pending''::text)')
    AND EXISTS (SELECT 1 FROM pg_indexes
                WHERE schemaname = current_schema() AND indexname = 'idx_outbox_messages_settled')
    AND EXISTS (SELECT 1 FROM pg_indexes
                WHERE schemaname = current_schema() AND indexname = 'uq_outbox_messages_dedupe'
                  AND indexdef LIKE 'CREATE UNIQUE INDEX%'
                  AND indexdef LIKE '%WHERE (dedupe_key IS NOT NULL)')
)::int;

-- The state-only guard: a BEFORE UPDATE ROW trigger backed by the function.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_proc p
            JOIN pg_namespace n ON n.oid = p.pronamespace
            WHERE p.proname = 'outbox_message_state_only' AND n.nspname = current_schema())
    AND EXISTS (SELECT 1 FROM pg_trigger t
                JOIN pg_class c ON c.oid = t.tgrelid
                WHERE t.tgname = 'outbox_message_state_only'
                  AND NOT t.tgisinternal
                  AND c.relname = 'outbox_messages'
                  AND (t.tgtype & 1) = 1     -- FOR EACH ROW
                  AND (t.tgtype & 2) = 2     -- BEFORE
                  AND (t.tgtype & 16) = 16)  -- UPDATE
)::int;
