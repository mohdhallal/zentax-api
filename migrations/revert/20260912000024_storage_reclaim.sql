-- Revert storage_reclaim
BEGIN;

DROP TABLE IF EXISTS storage_reclaim;

COMMIT;
