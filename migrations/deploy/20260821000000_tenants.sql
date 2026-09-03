-- Deploy tenants
BEGIN;

-- Partitioning helpers (ADR-0020: tables are partitioned by default). Both
-- helpers give every partition RLS ENABLED+FORCED with NO policies: the app
-- only ever names the parent (whose policies apply there), so direct access to
-- a partition — which would sidestep the parent's policies — is denied outright,
-- regardless of broad role grants.
CREATE FUNCTION create_hash_partitions(parent text, modulus int) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    part text;
BEGIN
    FOR i IN 0 .. modulus - 1 LOOP
        part := format('%s_p%s', parent, to_char(i, 'FM00'));
        EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES WITH (MODULUS %s, REMAINDER %s)',
                       part, parent, modulus, i);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', part);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', part);
    END LOOP;
END;
$$;

-- Idempotent monthly create-ahead for RANGE(time) streams: ensures partitions
-- from first_month through now()+months_ahead. Called by each range table's
-- migration AND by the migrate job on every run (the maintenance loop).
CREATE FUNCTION ensure_month_partitions(parent text, first_month date, months_ahead int) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    m date := date_trunc('month', first_month)::date;
    last_month date := (date_trunc('month', now()) + make_interval(months => months_ahead))::date;
    part text;
BEGIN
    WHILE m <= last_month LOOP
        part := format('%s_y%sm%s', parent, to_char(m, 'YYYY'), to_char(m, 'MM'));
        IF to_regclass(part) IS NULL THEN
            EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                           part, parent, m, (m + interval '1 month')::date);
            EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', part);
            EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', part);
        END IF;
        m := (m + interval '1 month')::date;
    END LOOP;
END;
$$;

-- The tenant registry. This is control-plane metadata (ADR-0005): it is NOT
-- itself tenant-scoped and carries no RLS — every tenant-scoped table
-- references it via tenant_id.
-- Not partitioned (ADR-0020 exception): small bounded registry, no growth risk.
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(63) NOT NULL UNIQUE,
    name TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants(slug);

COMMIT;
