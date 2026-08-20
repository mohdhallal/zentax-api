-- Verify entity_obligations
SELECT id, tenant_id, entity_id, obligation_type_id, jurisdiction, periodicity,
       deadline_rule, status, created_at, updated_at
FROM entity_obligations
WHERE false;
