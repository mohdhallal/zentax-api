-- Verify obligation_types
SELECT id, tenant_id, name, code, category, template, status, description, created_at, updated_at
FROM obligation_types
WHERE false;
