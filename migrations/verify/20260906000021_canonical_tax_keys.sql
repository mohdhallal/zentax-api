-- Verify canonical_tax_keys
BEGIN;

-- 1. The FORCE-RLS bracket the deploy opens must be closed again: all three
--    tables it lifted still FORCE row-level security (1/0 fails the script
--    otherwise). Checked first, before this script opens its own bracket.
SELECT 1 / (
    (SELECT COUNT(*)::int = 3
     FROM pg_class
     WHERE relname IN ('data_templates', 'workflow_tasks', 'task_instances')
       AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
       AND relforcerowsecurity)
)::int;

-- 2. No predefined template carries a fixture-era f-* field id any more.
--    data_templates FORCEs row-level security, which binds the owner this runs
--    as, so the check would see zero rows and pass vacuously; FORCE is lifted
--    for this transaction only (exactly as the deploy does) and restored before
--    COMMIT — and a failing assertion aborts the transaction, rolling the lift
--    back with it.
ALTER TABLE data_templates NO FORCE ROW LEVEL SECURITY;

DO $$
DECLARE
    stale INT;
BEGIN
    SELECT COUNT(*) INTO stale
    FROM data_templates dt, jsonb_array_elements(dt.fields) AS e(f)
    WHERE dt.category = 'predefined'
      AND dt.name IN ('VAT Return', 'Corporate Income Tax', 'Withholding Tax')
      AND e.f->>'id' LIKE 'f-%';
    IF stale > 0 THEN
        RAISE EXCEPTION 'canonical_tax_keys: % predefined template field(s) still carry a fixture-era f-* id', stale;
    END IF;
END $$;

ALTER TABLE data_templates FORCE ROW LEVEL SECURITY;

COMMIT;
