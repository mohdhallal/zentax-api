-- Verify tenants
SELECT id, slug, name, status, created_at, updated_at
FROM tenants
WHERE false;
