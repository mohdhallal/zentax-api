-- Revert security_events
BEGIN;

-- Dropping the partitioned table takes its partitions, indexes, policies and
-- the cloned trigger with it; the guard function is standalone and goes by name.
DROP TABLE IF EXISTS security_events;
DROP FUNCTION IF EXISTS security_event_append_only();

COMMIT;
