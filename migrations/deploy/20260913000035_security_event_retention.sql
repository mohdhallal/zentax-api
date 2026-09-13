-- Deploy security_event_retention
BEGIN;

-- THE RETENTION MECHANISM security_events WAS BUILT AROUND, ACTUALLY BUILT.
--
-- 20260913000031 partitioned this stream by month and said, in the table
-- comment and in its own prose, that ageing it out is a DROP of whole months.
-- ADR-0007 fixed the window at thirteen months and rested `client_ip` — the one
-- genuinely personal datum the product deliberately keeps — on it ("it stops
-- existing when its partition is dropped"). Nothing dropped anything. The
-- runbook told an operator the control was running, which is the worst version
-- of the gap: a record of a control that does not exist stops anyone looking
-- for the control. This is the missing half.
--
-- WHY A PARTITION DROP AND NOT A DELETE. Not a performance preference — a
-- DELETE here is not slower, it is impossible. security_events is append-only
-- twice over: its policy set has no DELETE policy at all (so under FORCE RLS a
-- DELETE matches nothing), and a BEFORE DELETE row trigger raises ZT031 for
-- every role, including one that bypasses RLS. A row-wise prune would have to
-- begin by dismantling the append-only guarantee the stream's integrity rests
-- on. DDL is subject to neither: dropping a partition fires no row trigger and
-- consults no policy, costs an unlink instead of one dead tuple per row, and
-- leaves no bloat for a VACUUM to chase. It is the only deletion this table
-- admits, and it was always the plan.
--
-- WHY THIS IS A FUNCTION AND NOT A `DELETE` IN THE API. The API connects as the
-- app role: NOSUPERUSER, NOBYPASSRLS, and — the part that matters here — NOT
-- THE OWNER of security_events. DROP TABLE requires ownership, so the job
-- cannot drop a partition directly and no grant can give it that (there is no
-- DROP privilege in Postgres). A SECURITY DEFINER function owned by the
-- migration role is the standard door, and what is conceded through it is
-- narrowed as far as the language allows:
--
--   * The TABLE IS HARD-CODED. There is no `parent text` parameter, so there is
--     no identifier a caller can supply and no dynamic SQL built from caller
--     input anywhere in the body.
--   * The CUTOFF IS NOT A PARAMETER EITHER. The caller passes a WINDOW IN
--     MONTHS and the function derives the instant from now(); a caller cannot
--     hand it an arbitrary point in time, let alone 'infinity'.
--   * The WINDOW HAS A FLOOR IN THE SCHEMA. Under twelve months is refused
--     (ZT032) — a SOC 2 audit period is the least this stream may be worth, and
--     the thirteen-month decision is that period plus a month of overlap. The
--     same floor is validated in config; this one is the copy no environment
--     variable can get past.
--
--   The residue is real and worth stating plainly: a principal holding the app
--   role's credentials can age out security_events partitions that are already
--   more than twelve months old — evidence that was, by the product's own
--   decision, within a month of being destroyed anyway. That principal already
--   holds full DML on every other table in the database. EXECUTE is left at the
--   default (PUBLIC) deliberately: migrate.sh grants on tables and sequences
--   only and never on functions, so a REVOKE here would leave the job unable to
--   call the one thing it exists to call, on a fresh install, with no error
--   until the first scheduled run.
--
-- WHAT IT MUST NEVER REMOVE — the same care platform/outbox's prune takes about
-- pending messages, which its RLS policy makes unreachable. Here the guards are
-- the WHERE clause and the bound arithmetic:
--
--   * security_events_default. It has NO upper bound, so it may hold a row from
--     any month including this one; dropping it would take every month with it.
--     Its bound reads 'DEFAULT', which the bound pattern below does not match,
--     so it is skipped by construction rather than by name.
--   * Anything whose bound this function cannot READ — an unbounded upper
--     (MAXVALUE), a hand-made partition, a future bound shape. An unparsed
--     bound yields NULL and NULL is never <= the cutoff, so the failure
--     direction is "keep it".
--   * Anything that is not a leaf partition OF THIS TABLE: the scan is
--     pg_inherits-joined to security_events, restricted to relkind 'r' in the
--     schema this function is pinned to.
--   * Any month that could still hold a row inside the window. A partition goes
--     only when its UPPER bound is at or before the cutoff — that is, when its
--     NEWEST POSSIBLE row is already past the window. A month can only be
--     dropped whole, so the promise "thirteen months" is a FLOOR: nothing is
--     destroyed before it is thirteen months old, and a row lives thirteen to
--     fourteen months depending on where in its month it fell. The rounding is
--     deliberately in the direction of keeping.
--
-- WHY IT ALSO CREATES. The drop half is worthless on its own. If a month has no
-- partition, its rows land in security_events_default — which retention must
-- never drop — and they are then beyond the reach of the control FOREVER, while
-- every record still says the stream ages out. 20260913000031 created fifteen
-- months ahead and left the top-up to deployment/docker/migrate.sh's
-- ensure_month_partitions loop, which was never told about this table. Rather
-- than make the control depend on a shell script remembering, the job holds the
-- whole invariant: THERE IS A PARTITION FOR EVERY MONTH INSIDE THE RETENTION
-- WINDOW, AND FOR EVERY MONTH OF THE RUN-AHEAD. Anchoring the loop at the start
-- of the window rather than at today is what makes a gap self-heal — a month
-- missed while the job was off, a snapshot restored from before a month — and
-- where a gap CANNOT heal, because the default partition already absorbed rows
-- for that month, the run reports it as blocked instead of failing. That report
-- is the operator's signal that some rows can no longer age out.
--
-- LOCKS. Both halves take ACCESS EXCLUSIVE on the parent, briefly, on a table
-- that sits on the hot unauthenticated login path. lock_timeout bounds the wait
-- so a busy moment costs a skipped month and a retry in six hours, never a
-- queue of blocked logins behind a maintenance job. The first successful
-- statement holds the lock for the rest of the transaction, so the remaining
-- months are free.
CREATE FUNCTION security_events_maintain_partitions(
    retention_months int,
    months_ahead     int
) RETURNS TABLE (action text, partition_name text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp
-- EVERY MONTH IN THIS FUNCTION IS A UTC MONTH, and the pin is load-bearing
-- rather than tidy. date_trunc('month', now()) truncates in the SESSION's
-- timezone, and a DATE literal becomes a TIMESTAMPTZ partition bound in the
-- session's timezone too — so on a connection whose TimeZone is not UTC this
-- function would compute a different cutoff AND create partitions whose bounds
-- are local midnights, which do not meet the UTC midnights every existing
-- partition was created with. Overlapping bounds are refused by Postgres and
-- reported here as blocked; NON-overlapping ones would leave a silent gap of a
-- few hours each month, and rows falling in it would land in the default
-- partition where retention can never reach them. Pinning removes both.
SET TimeZone = 'UTC'
AS $$
DECLARE
    -- The instant the window opens. Everything strictly below it is past the
    -- promise; nothing at or above it may be touched.
    cutoff     timestamptz;
    first_m    date;
    last_m     date;
    m          date;
    part       text;
    rec        record;
    grant_row  record;
    upper_txt  text;
    -- The bound as pg_get_expr renders it for a single-column RANGE partition:
    --   FOR VALUES FROM ('2025-07-01 00:00:00+00') TO ('2025-08-01 00:00:00+00')
    -- DEFAULT and MAXVALUE do not match, which is exactly the intent.
    bound_re   text := '^FOR VALUES FROM \(''([^'']+)''\) TO \(''([^'']+)''\)$';
BEGIN
    IF retention_months IS NULL OR retention_months < 12 THEN
        RAISE EXCEPTION 'security_events retention must be at least 12 months, got %', retention_months
            USING ERRCODE = 'ZT032',
                  HINT = 'thirteen months is the decision (ADR-0007: a SOC 2 audit period plus a month of overlap); twelve is the floor the schema will not go under';
    END IF;
    IF retention_months > 120 THEN
        RAISE EXCEPTION 'security_events retention of % months is beyond what this stream is for', retention_months
            USING ERRCODE = 'ZT032',
                  HINT = 'the authentication stream is operational security evidence whose value decays; the statutory decade belongs to the audit trail, not here';
    END IF;
    IF months_ahead IS NULL OR months_ahead < 1 OR months_ahead > 60 THEN
        RAISE EXCEPTION 'security_events partition run-ahead must be between 1 and 60 months, got %', months_ahead
            USING ERRCODE = 'ZT032';
    END IF;

    -- A month at a time: the drop set only changes on the first of a month, so
    -- every other run of the day has nothing to do and says nothing.
    cutoff  := date_trunc('month', now()) - make_interval(months => retention_months);
    first_m := cutoff::date;
    last_m  := (date_trunc('month', now()) + make_interval(months => months_ahead))::date;

    PERFORM set_config('lock_timeout', '5s', true);

    -- ── Every month inside the window, and the run-ahead, has a partition ────
    m := first_m;
    WHILE m <= last_m LOOP
        part := format('security_events_y%sm%s', to_char(m, 'YYYY'), to_char(m, 'MM'));
        IF to_regclass('public.' || quote_ident(part)) IS NULL THEN
            BEGIN
                EXECUTE format(
                    'CREATE TABLE public.%I PARTITION OF public.security_events FOR VALUES FROM (%L) TO (%L)',
                    part, m, (m + interval '1 month')::date);
                -- RLS on the leaf as well, exactly as ensure_month_partitions
                -- and the creating migration do: the parent's policies govern
                -- access through the parent, and these two make a direct read
                -- of the leaf no easier than a read of the table.
                EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', part);
                EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', part);
                -- AND THE PARENT'S PRIVILEGES, EXACTLY. A partition created at
                -- runtime is a brand-new table owned by the function's DEFINER,
                -- and Postgres gives a new table no grants at all — while
                -- migrate.sh's `GRANT ... ON ALL TABLES` only reaches the
                -- partitions that existed when the deploy ran. Access ROUTED
                -- through the parent does not consult the leaf, so the hole is
                -- invisible until something addresses a partition directly, and
                -- then it is a hard permission-denied on a table nobody
                -- remembers creating. Copying the parent's ACL is what migrate.sh
                -- would have done and keeps a runtime-created month
                -- indistinguishable from a deploy-created one. The privilege
                -- names come from the catalogue and are whitelisted, and the
                -- grantee is rendered by regrole (which quotes what needs it),
                -- so neither is a string a caller could reach.
                FOR grant_row IN
                    SELECT CASE WHEN acl.grantee = 0 THEN 'PUBLIC'
                                ELSE acl.grantee::regrole::text END AS grantee,
                           acl.privilege_type AS privilege
                      FROM pg_class parent, aclexplode(parent.relacl) acl
                     WHERE parent.oid = 'public.security_events'::regclass
                       AND acl.privilege_type IN
                           ('SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER')
                LOOP
                    EXECUTE format('GRANT %s ON public.%I TO %s',
                                   grant_row.privilege, part, grant_row.grantee);
                END LOOP;
                action := 'created'; partition_name := part; RETURN NEXT;
            EXCEPTION
                WHEN lock_not_available THEN
                    -- The login path is busy. Six hours from now it will not be.
                    action := 'deferred'; partition_name := part; RETURN NEXT;
                WHEN others THEN
                    -- Almost always: security_events_default already holds rows
                    -- for this month, so the month can no longer be carved out
                    -- of it. Those rows are now outside the reach of retention,
                    -- which is a thing an operator has to be told.
                    action := 'blocked'; partition_name := part; RETURN NEXT;
            END;
        END IF;
        m := (m + interval '1 month')::date;
    END LOOP;

    -- ── Every month wholly past the window goes ─────────────────────────────
    FOR rec IN
        SELECT c.relname AS relname,
               pg_get_expr(c.relpartbound, c.oid) AS bound
        FROM pg_class c
        JOIN pg_inherits i ON i.inhrelid = c.oid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE i.inhparent = 'public.security_events'::regclass
          AND c.relkind = 'r'
          AND n.nspname = 'public'
        ORDER BY c.relname
    LOOP
        upper_txt := (regexp_match(rec.bound, bound_re))[2];
        -- DEFAULT, MAXVALUE, or any bound shape this function does not
        -- understand: NULL, and NULL is never past the cutoff.
        CONTINUE WHEN upper_txt IS NULL;
        -- The newest row this month could hold is still inside the window.
        CONTINUE WHEN upper_txt::timestamptz > cutoff;
        BEGIN
            EXECUTE format('DROP TABLE public.%I', rec.relname);
            action := 'dropped'; partition_name := rec.relname; RETURN NEXT;
        EXCEPTION WHEN lock_not_available THEN
            action := 'deferred'; partition_name := rec.relname; RETURN NEXT;
        END;
    END LOOP;

    RETURN;
END;
$$;

COMMENT ON FUNCTION security_events_maintain_partitions(int, int) IS
    'ADR-0007 retention for ADR-0008 stream 2: creates a partition for every month inside the retention window and the run-ahead, and drops every month wholly past the window. SECURITY DEFINER because the app role does not own the table; the table is hard-coded, the cutoff is derived from now(), and a window under 12 months is refused (ZT032). Never touches security_events_default or any bound it cannot read.';

COMMIT;
