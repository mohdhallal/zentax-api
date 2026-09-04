-- Revert fiscal_calendar
BEGIN;

ALTER TABLE task_instances
    DROP COLUMN IF EXISTS payment_deadline;

ALTER TABLE entities
    DROP COLUMN IF EXISTS fiscal_week_end_day,
    DROP COLUMN IF EXISTS fiscal_year_end_rule,
    DROP COLUMN IF EXISTS custom_periods;

COMMIT;
