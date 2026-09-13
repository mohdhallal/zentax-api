-- Revert audit_export_segments
--
-- Drops the export cursor/receipt ledger. The exported OBJECTS are untouched —
-- they are the point of the feature and live in the object store, where a
-- hosted deployment holds them under Object Lock and cannot delete them at all.
-- After this revert the next export of each tenant starts from seq 1 again and
-- re-writes segments under keys that already exist; on a versioned/locked
-- bucket that adds versions rather than replacing evidence.

BEGIN;

DROP TRIGGER IF EXISTS audit_export_segment_state_only ON audit_export_segments;
DROP FUNCTION IF EXISTS audit_export_segment_state_only();
DROP TABLE IF EXISTS audit_export_segments;

COMMIT;
