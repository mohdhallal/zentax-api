-- Verify entities
SELECT id, tenant_id, parent_entity_id, name, legal_name, country, tax_residency,
       fiscal_calendar_pattern, financial_year_end, status, created_at, updated_at
FROM entities
WHERE false;
