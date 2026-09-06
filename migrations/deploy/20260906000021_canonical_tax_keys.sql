-- Deploy canonical_tax_keys
BEGIN;

-- The predefined data templates (VAT Return / Corporate Income Tax /
-- Withholding Tax) stored their figures under fixture-era field ids
-- (f-vat-output, f-cit-due, f-wht-amount, ...) that no report ever read, so an
-- instance bound to a predefined template could never feed the tax-financial
-- report. The field ids are now the canonical tax-data keys (shared/taxkeys:
-- outputVat, taxLiability, whtAmount, ...). This migration renames the ids on
-- every tenant's predefined rows and the same keys inside the tax data of the
-- instances bound to those templates (directly, or through their workflow
-- task), so recorded values stay addressable and mandatory-field checks keep
-- passing. Custom templates and free-form tax data are untouched.
--
-- The three tables read or written here FORCE row-level security, which
-- binds the OWNER too (the RDS master user the migrate task runs as is an
-- owner, not a superuser): the tenant policy would silently match zero rows
-- (and hide every workflow task, so no instance would resolve its template).
-- FORCE is lifted for the duration of this transaction only — the owner then
-- bypasses the policy — and restored before COMMIT; the app role is never
-- affected.

ALTER TABLE data_templates NO FORCE ROW LEVEL SECURITY;
ALTER TABLE workflow_tasks NO FORCE ROW LEVEL SECURITY;
ALTER TABLE task_instances NO FORCE ROW LEVEL SECURITY;

CREATE TEMP TABLE canonical_key_map (old_key TEXT PRIMARY KEY, new_key TEXT NOT NULL) ON COMMIT DROP;
INSERT INTO canonical_key_map (old_key, new_key) VALUES
    ('f-vat-sales',   'salesTotal'),
    ('f-vat-output',  'outputVat'),
    ('f-vat-input',   'inputVat'),
    ('f-vat-net',     'netVat'),
    ('f-cit-pbt',     'profitBeforeTax'),
    ('f-cit-adj',     'adjustments'),
    ('f-cit-taxable', 'taxableIncome'),
    ('f-cit-rate',    'taxRate'),
    ('f-cit-due',     'taxLiability'),
    ('f-wht-base',    'whtBase'),
    ('f-wht-rate',    'whtRate'),
    ('f-wht-amount',  'whtAmount');

-- 1. Instances bound to a predefined template: rename the keys in tax_data
--    (values kept; keys the map does not know pass through unchanged).
WITH predefined AS (
    SELECT tenant_id, id
    FROM data_templates
    WHERE category = 'predefined'
      AND name IN ('VAT Return', 'Corporate Income Tax', 'Withholding Tax')
),
bound AS (
    SELECT ti.tenant_id, ti.id
    FROM task_instances ti
    LEFT JOIN workflow_tasks wt ON wt.tenant_id = ti.tenant_id AND wt.id = ti.workflow_task_id
    JOIN predefined p ON p.tenant_id = ti.tenant_id
                     AND p.id = COALESCE(ti.data_template_id, wt.data_template_id)
    WHERE ti.tax_data IS NOT NULL
      AND EXISTS (SELECT 1 FROM jsonb_object_keys(ti.tax_data) k JOIN canonical_key_map m ON m.old_key = k)
)
UPDATE task_instances ti
SET tax_data = (
        SELECT COALESCE(jsonb_object_agg(COALESCE(m.new_key, kv.key), kv.value), '{}'::jsonb)
        FROM jsonb_each(ti.tax_data) kv
        LEFT JOIN canonical_key_map m ON m.old_key = kv.key
    ),
    updated_at = NOW()
FROM bound b
WHERE ti.tenant_id = b.tenant_id AND ti.id = b.id;

-- 2. The predefined templates themselves: rename the field ids in place
--    (field order, labels, flags and validation kept).
UPDATE data_templates dt
SET fields = (
        SELECT jsonb_agg(
                   CASE WHEN m.new_key IS NULL THEN e.f ELSE jsonb_set(e.f, '{id}', to_jsonb(m.new_key)) END
                   ORDER BY e.ord)
        FROM jsonb_array_elements(dt.fields) WITH ORDINALITY AS e(f, ord)
        LEFT JOIN canonical_key_map m ON m.old_key = e.f->>'id'
    ),
    updated_at = NOW()
WHERE dt.category = 'predefined'
  AND dt.name IN ('VAT Return', 'Corporate Income Tax', 'Withholding Tax')
  AND EXISTS (
        SELECT 1
        FROM jsonb_array_elements(dt.fields) e(f)
        JOIN canonical_key_map m ON m.old_key = e.f->>'id');

ALTER TABLE task_instances FORCE ROW LEVEL SECURITY;
ALTER TABLE workflow_tasks FORCE ROW LEVEL SECURITY;
ALTER TABLE data_templates FORCE ROW LEVEL SECURITY;

COMMIT;
