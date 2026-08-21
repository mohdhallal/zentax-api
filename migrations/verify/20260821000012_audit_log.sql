-- Verify audit_log
SELECT event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
       occurred_at, request_id, details, prev_hash, hash
FROM audit_log
WHERE false;
