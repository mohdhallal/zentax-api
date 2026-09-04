-- Deploy fiscal_calendar
BEGIN;

-- ADR-0023: fiscal calendars for every pattern + the payment deadline.
--
-- entities: the week anchor for the week-based patterns (445 / 454 / 544 /
-- 13-period / weekly) — which weekday a week ends on and whether the fiscal
-- year ends on the LAST such weekday on/before financial_year_end or the
-- NEAREST one — and the explicit period list of the `custom` pattern
-- ([{code, name, startDate MM-DD, endDate MM-DD}], validated by the API).
-- Existing rows keep working through the defaults (Saturday / nearest — the
-- NRF convention); custom_periods stays NULL for every other pattern.
--
-- task_instances: payment_deadline, derived at workflow start from the entity
-- obligation's payment rule (offset, fixed dates, or "same as filing"). A
-- legal date-only value (ADR-0002). Nullable so rows generated before this
-- migration stay valid; the generator always sets it from now on.
--
-- workflow_tasks.due_date_reference gains the value 'payment_deadline'; the
-- column has no CHECK constraint (the vocabulary is enforced by the API), so
-- nothing changes here.
--
-- Additive ADD COLUMN on HASH(tenant_id) partitioned tables propagates to
-- every partition without a rewrite (ADR-0013 expand step, ADR-0020).
ALTER TABLE entities
    ADD COLUMN fiscal_week_end_day VARCHAR(9) NOT NULL DEFAULT 'saturday'
        CONSTRAINT entities_fiscal_week_end_day_check CHECK (
            fiscal_week_end_day IN ('monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday')
        ),
    ADD COLUMN fiscal_year_end_rule VARCHAR(10) NOT NULL DEFAULT 'nearest'
        CONSTRAINT entities_fiscal_year_end_rule_check CHECK (
            fiscal_year_end_rule IN ('last', 'nearest')
        ),
    ADD COLUMN custom_periods JSONB;

ALTER TABLE task_instances
    ADD COLUMN payment_deadline DATE;

COMMIT;
