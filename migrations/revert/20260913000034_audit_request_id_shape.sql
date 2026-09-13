-- Revert audit_request_id_shape
BEGIN;

-- Dropping this reopens the door: the column goes back to accepting whatever a
-- caller puts in X-Request-Id, into a hash-covered table that is exported to a
-- bucket nothing can delete from. The middleware still refuses a malformed
-- header, so the door is shut in code either way — this constraint is the half
-- that a future code path cannot walk around.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_request_id_shape;

COMMENT ON COLUMN audit_log.request_id IS NULL;

COMMIT;
