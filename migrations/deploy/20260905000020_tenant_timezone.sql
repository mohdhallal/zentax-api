-- Deploy tenant_timezone
BEGIN;

-- Tenant display / scheduling timezone (ADR-0003, ADR-0023 §6): an IANA zone
-- name, default UTC, edited by tenant admins through PUT /tenant. Instants stay
-- UTC at rest (timestamptz); this column decides what "today" means for the
-- server-side overdue / missed classification and, later, for digests. The
-- API validates the name with Go's tz database before it is stored; the CHECK
-- only guards against an empty string. tenants is the non-partitioned
-- control-plane registry (ADR-0020 exception) and stays that way — a plain
-- additive ADD COLUMN with a default takes no rewrite.
ALTER TABLE tenants
    ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT 'UTC' CHECK (timezone <> '');

COMMIT;
