-- Deploy outbox_messages
BEGIN;

-- The transactional outbox: every message the product owes a human, written on
-- the SAME transaction as the change that owes it.
--
-- Why an outbox at all. A deadline reminder is the product's core promise, and
-- the two failure modes of sending it inline are both unacceptable: send before
-- commit and a rolled-back mutation still mailed the customer; send after
-- commit and a crash (or a provider outage, or a five-second SMTP timeout
-- inside an HTTP handler) loses the notification with no record that it was
-- ever owed. A row here commits or rolls back WITH the mutation, so the
-- mutation and its notification can never disagree, and a provider outage costs
-- a retry rather than a lost message.
--
-- Delivery is at-least-once, never exactly-once (no distributed transaction
-- spans Postgres and an SMTP provider). Two things make a duplicate harmless:
--   * dedupe_key — the producer's idempotency key, unique per tenant, so
--     re-running a producer (a scheduled reminder pass that overlaps its
--     predecessor, a retried API call) enqueues nothing new; and
--   * payload — a SNAPSHOT taken at enqueue time. The rendered mail is derived
--     from this row and never re-read from live data, so a redelivery is the
--     identical mail again, not a second, different one.
--
-- Deliberately NOT partitioned (the ADR-0020 exception that storage_reclaim and
-- user_grants already take): a drained queue, bounded by the prune job, whose
-- claim path is a single hot index. Range partitioning would also oblige
-- migrate.sh to top partitions up for a table that is supposed to stay small.
CREATE TABLE outbox_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,

    -- WHAT to send. channel names the transport seam (email today); template
    -- names the message kind the mail layer renders ('deadline.reminder',
    -- 'member.invited'); payload is the frozen variable set for that render.
    channel VARCHAR(20) NOT NULL DEFAULT 'email'
        CHECK (channel IN ('email')),
    template VARCHAR(60) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- WHO it is for. The only personal datum on the row (ADR-0007): it exists
    -- for as long as the message is undelivered plus the prune window, and it
    -- NEVER reaches the audit trail — an undeliverable entry carries the
    -- message id and a reason class, never the address.
    recipient TEXT NOT NULL,

    -- The producer's idempotency key. NULL means "not deduplicated"; a value is
    -- unique per tenant (partial unique index below).
    dedupe_key TEXT,

    -- WHEN it became due (the instant the message was supposed to go out —
    -- immutable, so lateness is measurable) and WHEN to try next (moved by the
    -- claim lease and by backoff). They are equal at enqueue.
    due_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts SMALLINT NOT NULL DEFAULT 0,

    -- Terminal state. 'dead' is a message given up on: a permanent rejection,
    -- or the attempt budget spent. last_error is the operator's reason (never
    -- rendered to a customer).
    status VARCHAR(10) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'dead')),
    last_error TEXT,
    sent_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- ADR-0008 actor-by-ID: whose mutation owed this message. NULL when a
    -- scheduled producer queued it with no acting user. The delivery runner
    -- attributes an undeliverable audit entry to this actor.
    created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,

    -- The state machine, spelled out so no writer can invent a half-settled row.
    CONSTRAINT outbox_messages_state CHECK (
        (status = 'pending' AND sent_at IS NULL     AND failed_at IS NULL)
     OR (status = 'sent'    AND sent_at IS NOT NULL AND failed_at IS NULL)
     OR (status = 'dead'    AND sent_at IS NULL     AND failed_at IS NOT NULL)
    ),
    CONSTRAINT outbox_messages_attempts_sane CHECK (attempts >= 0)
);

-- The runner's working set: due, unsettled, oldest schedule first. Every claim
-- is this index and nothing else.
CREATE INDEX idx_outbox_messages_due ON outbox_messages (next_attempt_at, id)
    WHERE status = 'pending';

-- The prune job's working set: settled rows, oldest settlement first.
CREATE INDEX idx_outbox_messages_settled ON outbox_messages (updated_at)
    WHERE status <> 'pending';

-- A tenant's own view of what it was sent (an operator screen, a support
-- question), newest first.
CREATE INDEX idx_outbox_messages_tenant ON outbox_messages (tenant_id, created_at DESC);

-- Producer idempotency: one live row per (tenant, key). This is what makes a
-- re-run of a producer a no-op rather than a second mail.
CREATE UNIQUE INDEX uq_outbox_messages_dedupe ON outbox_messages (tenant_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

-- ── Row-level security ──────────────────────────────────────────────────────
--
-- The table is tenant-scoped like every other domain table (ADR-0004), but the
-- delivery loop is the one reader in the system that is NOT acting for a
-- tenant: it drains every tenant's due messages in one pass. The app role is
-- NOSUPERUSER + NOBYPASSRLS (deployment/docker/migrate.sh asserts it), so the
-- runner cannot simply see past the policy — and it must not, because "the job
-- that ignores RLS" is exactly the hole a tenant-isolation model dies of.
--
-- So the cross-tenant path is an EXPLICIT, NARROW second door: a transaction
-- that sets the `app.outbox_runner` GUC with set_config(..., is_local => true)
-- (SET LOCAL — transaction-scoped, hence pooling-safe) matches the runner
-- policies below. Nothing on a request path ever sets that GUC: the Tx seam
-- (platform/database.Exec.WithinTransaction) binds only app.tenant_id and
-- app.user_id, both from the authenticated session, and no handler can reach
-- set_config. The GUC is set in exactly one place in the codebase —
-- platform/outbox.Store.withinRunnerTx.
--
-- What the second door does NOT open:
--   * no INSERT policy for the runner — it can never AUTHOR a message; a
--     message is only ever written by a use case on its own tenant transaction;
--   * DELETE only for SETTLED rows — the prune job can never destroy work that
--     has not yet been delivered or given up on.
ALTER TABLE outbox_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_messages FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON outbox_messages
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY runner_read ON outbox_messages FOR SELECT
    USING (current_setting('app.outbox_runner', true) = 'on');

CREATE POLICY runner_settle ON outbox_messages FOR UPDATE
    USING (current_setting('app.outbox_runner', true) = 'on')
    WITH CHECK (current_setting('app.outbox_runner', true) = 'on');

CREATE POLICY runner_prune ON outbox_messages FOR DELETE
    USING (current_setting('app.outbox_runner', true) = 'on' AND status <> 'pending');

-- ── What an update may move ─────────────────────────────────────────────────
--
-- The runner holds a cross-tenant UPDATE door, so the policy alone cannot say
-- what it may write — WITH CHECK sees only the NEW row and can compare nothing
-- to the old one. This trigger draws that line: an update may move DELIVERY
-- STATE and nothing else. Re-addressing a queued message (another tenant,
-- another recipient, another payload) is refused, and so is re-opening a
-- settled one — a message given up on is history; a fresh attempt is a fresh
-- message, with its own dedupe key and its own audit.
--
-- SECURITY INVOKER (the default): a psql session and a superuser are bound by
-- it too.
CREATE FUNCTION outbox_message_state_only() RETURNS trigger
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

CREATE TRIGGER outbox_message_state_only
    BEFORE UPDATE ON outbox_messages
    FOR EACH ROW EXECUTE FUNCTION outbox_message_state_only();

COMMIT;
