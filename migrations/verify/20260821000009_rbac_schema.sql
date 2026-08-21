-- Verify rbac_schema
SELECT id, tenant_id, user_id, role, scope_entity_id, created_at FROM user_grants WHERE false;
SELECT tenant_id, ancestor_id, descendant_id, depth FROM entity_closure WHERE false;
