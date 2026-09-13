-- Deploy security_events
BEGIN;

-- ADR-0008 STREAM 2: the authentication event stream.
--
-- WHY IT CANNOT BE THE DOMAIN TRAIL. audit_log answers "who changed what to the
-- tax data": it REQUIRES a tenant (RLS keys on app.tenant_id and the append
-- policy refuses a tenant-less row) and it REQUIRES an actor (actor_id is NOT
-- NULL). The single most important authentication event has neither — a failed
-- login for an address nobody recognises is an unidentified stranger acting on
-- no tenant — so the events a breach investigation starts from are precisely
-- the ones that table can never hold. Both correlators are therefore NULLABLE
-- here, and that nullability is the whole reason the stream exists separately.
--
-- WHAT IT IS DESIGNED TO ANSWER, column by column:
--   * WHO authenticated          -> principal_id (and actor_id when a third
--                                   party acted on the principal)
--   * WHO tried, when unknown    -> subject_digest (see below)
--   * FROM WHERE                 -> client_ip
--   * WHEN                       -> occurred_at (and the partition it lands in)
--   * BY WHAT METHOD             -> method
--   * WHAT HAPPENED              -> event + outcome + reason
--
-- THE SUBMITTED ADDRESS IS NEVER STORED. An address that resolves to no account
-- is an unauthenticated stranger's personal data, and this ledger is append-only
-- — it is not an erasure boundary and can never be redacted after the fact. So
-- an identified attempt correlates by principal_id, and an unidentified one by
-- subject_digest: a KEYED (HMAC-SHA256) digest of the normalised address, under
-- a key derived from AUTH_ENCRYPTION_KEY and held outside the database. An
-- investigator can still count attempts against one address (digest the address,
-- select on it); the store holds nothing that can be read back into an address,
-- and an unkeyed rainbow table over the world's e-mail addresses buys nothing.
-- The CHECK is what makes "never in the clear" structural rather than a promise:
-- a CHAR(64) constrained to lowercase hex cannot hold an address.
--
-- NO COLUMN CAN HOLD CALLER TEXT. Every column is a code constant (event,
-- outcome, method, reason), a UUID, an INET, a timestamp, or that 64-hex digest.
-- There is deliberately no free-form details JSONB: the domain trail's PII rule
-- is enforced there by review, and here by the type system.
--
-- NO HASH CHAIN, DELIBERATELY. audit_log's chain is PER TENANT, which is what
-- keeps its serialised appends parallel across tenants. This stream's most
-- important rows have no tenant, so the equivalent here would be ONE global
-- chain — a cluster-wide advisory lock taken on every login, i.e. a lock on the
-- hottest unauthenticated path in the product. The integrity floor here is
-- append-only at the database (twice over, below); the tamper-evidence tier is
-- shipping the stream off-box to the central security log store, which is where
-- ADR-0008 puts stream 2 in the first place.
--
-- Partitioned by RANGE(occurred_at), monthly (ADR-0020) — and here partitioning
-- is not only the growth story, it is the RETENTION mechanism. The table is
-- append-only, so no row can ever be deleted or rewritten; ageing the stream out
-- is a DETACH/DROP of whole months. That is what bounds how long client_ip — the
-- one personal datum on the row — exists at all. Retention: 13 months (a SOC 2
-- audit period plus a month of overlap). The create-ahead below runs 15 months
-- out so the _default backstop is not reached before the migrate job's
-- maintenance loop learns about this table.
CREATE TABLE security_events (
    event_id       UUID NOT NULL DEFAULT gen_random_uuid(),
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- WHAT HAPPENED. A closed vocabulary owned by platform/securityevent; the
    -- outcome and method sets are small and closed enough to pin here.
    event          VARCHAR(40) NOT NULL,
    outcome        VARCHAR(10) NOT NULL CHECK (outcome IN ('success', 'failure')),
    method         VARCHAR(20) NOT NULL
        CHECK (method IN ('password', 'totp', 'session', 'api_token')),
    -- The reason CLASS a refusal fell into (never a message, never free text).
    -- It records what the response deliberately withholds: the answer to the
    -- caller is one generic refusal, the answer to the operator is here.
    reason         VARCHAR(40),

    -- WHO. All four are nullable: the events that matter most precede
    -- identification. principal_id is whose access the event is ABOUT; actor_id
    -- is who caused it when that is somebody else (an administrator disabling a
    -- member), and is NULL when the principal acted on itself.
    --
    -- No foreign keys, on purpose. The stream must outlive what it references:
    -- ADR-0007 erasure scrubs the user record and the event stays (resolving to
    -- an opaque id that no longer identifies a person), an off-boarded tenant's
    -- security history is exactly what an incident review still needs, and a
    -- tenant-less row has nothing to point at.
    tenant_id      UUID,
    principal_id   UUID,
    actor_id       UUID,
    session_id     UUID,

    subject_digest CHAR(64) CHECK (subject_digest ~ '^[0-9a-f]{64}$'),

    client_ip      INET,
    -- The correlation id, and a UUID rather than TEXT for a reason: the request
    -- id is caller-supplied when an X-Request-Id header is present, so a TEXT
    -- column here would be the one door through which arbitrary caller text
    -- could reach an append-only store. A value that is not a UUID is dropped.
    request_id     UUID,

    PRIMARY KEY (event_id, occurred_at),

    -- One correlator, never two. If the address resolved to an account, the
    -- account IS the correlation and a digest of the address would only add a
    -- second pseudonym for a person already named by id.
    CONSTRAINT security_events_one_correlator
        CHECK (principal_id IS NULL OR subject_digest IS NULL)
) PARTITION BY RANGE (occurred_at);

COMMENT ON TABLE security_events IS
    'ADR-0008 stream 2: authentication events. Tenant and actor are optional — the events that matter most precede identification. Append-only; retention by partition drop.';
COMMENT ON COLUMN security_events.subject_digest IS
    'Keyed HMAC-SHA256 (hex) of the submitted address when it resolved to no account. The address itself is NEVER stored; the key lives outside the database.';
COMMENT ON COLUMN security_events.client_ip IS
    'The caller''s network address. Personal data (ADR-0015), kept deliberately: a security stream that cannot say "from where" cannot serve an incident. Bounded by partition-drop retention, not by obfuscation.';
COMMENT ON COLUMN security_events.reason IS
    'The refusal class the operator needs and the response deliberately withholds. A closed set of code constants — never a message.';

SELECT ensure_month_partitions('security_events', DATE '2026-09-01', 15);
CREATE TABLE security_events_default PARTITION OF security_events DEFAULT;
ALTER TABLE security_events_default ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_events_default FORCE ROW LEVEL SECURITY;

-- The four questions, indexed. "Everything between T1 and T2" needs no index of
-- its own: it is a partition scan by construction.
CREATE INDEX idx_security_events_principal ON security_events (principal_id, occurred_at DESC)
    WHERE principal_id IS NOT NULL;
CREATE INDEX idx_security_events_subject ON security_events (subject_digest, occurred_at DESC)
    WHERE subject_digest IS NOT NULL;
CREATE INDEX idx_security_events_client_ip ON security_events (client_ip, occurred_at DESC)
    WHERE client_ip IS NOT NULL;
CREATE INDEX idx_security_events_tenant ON security_events (tenant_id, occurred_at DESC)
    WHERE tenant_id IS NOT NULL;

-- ── Row-level security ──────────────────────────────────────────────────────
--
-- APPEND-ONLY, like audit_log: policies exist ONLY for INSERT and SELECT, so
-- with FORCE RLS an UPDATE or DELETE matches no policy and affects zero rows,
-- for the table owner as well as the app role (NOSUPERUSER + NOBYPASSRLS).
--
-- The INSERT policy is unconditional, and that is forced by what the stream is
-- for: the writer runs BEFORE any tenant is known — /auth/login declares no
-- transaction and binds no app.tenant_id — so a tenant-keyed WITH CHECK would
-- refuse exactly the failed-login row this table exists to keep. Nothing is
-- conceded by it: the only writer is platform/securityevent, no request can
-- choose a tenant_id (it is derived from the resolved account, never from the
-- body), and the rows are unreadable through this policy anyway.
--
-- READS are the narrow half. A tenant sees its own identified events and
-- nothing else; the tenant-less rows — every probe against an address that
-- resolves to no account — are invisible to every tenant, which is right: they
-- belong to no tenant and naming them to one would leak another customer's
-- exposure. The cross-tenant door is an EXPLICIT GUC (app.security_stream), set
-- in exactly one place in the codebase (platform/securityevent.Reader.within),
-- never on a request path: the Tx seam binds only app.tenant_id and app.user_id,
-- and no handler can reach set_config.
ALTER TABLE security_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_events FORCE ROW LEVEL SECURITY;

CREATE POLICY append ON security_events FOR INSERT
    WITH CHECK (true);

CREATE POLICY tenant_read ON security_events FOR SELECT
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY stream_read ON security_events FOR SELECT
    USING (current_setting('app.security_stream', true) = 'on');

-- ── Append-only, the second time ────────────────────────────────────────────
--
-- audit_log is append-only twice over: its RLS policy set, plus a REVOKE UPDATE,
-- DELETE that deployment/docker/migrate.sh re-applies on every run. That second
-- belt cannot be expressed for this table from here — migrate.sh re-grants
-- UPDATE and DELETE on ALL TABLES on every run, so a REVOKE written in this
-- migration would be undone by the next deploy. A trigger cannot be, and it also
-- covers the case RLS does not: a session that DOES bypass RLS (a superuser, a
-- restored snapshot opened in psql) is still refused by SECURITY INVOKER.
CREATE FUNCTION security_event_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'security_events is append-only: % is not permitted', TG_OP
        USING ERRCODE = 'ZT031',
              HINT = 'an authentication event is evidence; correct the record by appending, and age the stream out by dropping whole partitions';
END;
$$;

COMMENT ON FUNCTION security_event_append_only() IS
    'security_events: refuses every UPDATE and DELETE (SQLSTATE ZT031), including from a role that bypasses RLS.';

CREATE TRIGGER security_event_append_only
    BEFORE UPDATE OR DELETE ON security_events
    FOR EACH ROW EXECUTE FUNCTION security_event_append_only();

COMMIT;
