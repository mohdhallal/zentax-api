-- Revert security_event_retention
BEGIN;

-- Only the door is removed. The partitions this function created stay, and the
-- ones it dropped are gone for good — a revert cannot un-age a stream, and
-- pretending otherwise by recreating empty months would be worse than leaving
-- the shape as it is.
DROP FUNCTION IF EXISTS security_events_maintain_partitions(int, int);

COMMIT;
