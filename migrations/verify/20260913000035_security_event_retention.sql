-- Verify security_event_retention

-- The function exists, returns a set, and is the SECURITY DEFINER door the app
-- role needs (it does not own security_events, and there is no DROP privilege
-- to grant).
SELECT 1 / (
    (SELECT p.prosecdef AND p.proretset
       FROM pg_proc p
       JOIN pg_namespace n ON n.oid = p.pronamespace
      WHERE p.proname = 'security_events_maintain_partitions'
        AND n.nspname = current_schema())
)::int;

-- Its search_path is pinned (what makes a SECURITY DEFINER function safe to
-- leave executable) and so is its timezone: every month it reasons about and
-- every partition bound it writes must be a UTC month, or a connection with a
-- local TimeZone would leave a few hours of each month in the default partition
-- where retention can never reach them.
SELECT 1 / (
    (SELECT EXISTS (SELECT 1 FROM unnest(p.proconfig) AS c WHERE c LIKE 'search_path=%')
        AND EXISTS (SELECT 1 FROM unnest(p.proconfig) AS c WHERE lower(c) = 'timezone=utc')
       FROM pg_proc p
       JOIN pg_namespace n ON n.oid = p.pronamespace
      WHERE p.proname = 'security_events_maintain_partitions'
        AND n.nspname = current_schema())
)::int;

-- No caller-supplied identifier reaches it: the only arguments are two
-- integers, so the table it acts on and the instant it compares against are
-- both the function's own.
SELECT 1 / (
    (SELECT pg_get_function_arguments(p.oid) = 'retention_months integer, months_ahead integer'
       FROM pg_proc p
       JOIN pg_namespace n ON n.oid = p.pronamespace
      WHERE p.proname = 'security_events_maintain_partitions'
        AND n.nspname = current_schema())
)::int;

-- The floor is in the schema, not only in config: a window under a SOC 2 audit
-- period is refused (ZT032) rather than honoured.
DO $$
BEGIN
    PERFORM * FROM security_events_maintain_partitions(11, 3);
    RAISE EXCEPTION 'an 11-month retention window was accepted; the floor is not enforced';
EXCEPTION WHEN sqlstate 'ZT032' THEN
    NULL;
END;
$$;

-- The backstop the drop half must never reach is still there.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_class c
             JOIN pg_inherits i ON i.inhrelid = c.oid
            WHERE i.inhparent = 'security_events'::regclass
              AND c.relname = 'security_events_default'
              AND pg_get_expr(c.relpartbound, c.oid) = 'DEFAULT')
)::int;
