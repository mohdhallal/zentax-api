-- Verify fiscal_calendar
SELECT fiscal_week_end_day, fiscal_year_end_rule, custom_periods
FROM entities
WHERE false;

SELECT payment_deadline
FROM task_instances
WHERE false;
