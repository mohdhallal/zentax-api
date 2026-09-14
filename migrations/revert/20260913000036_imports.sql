-- Revert imports
--
-- Drops spreadsheet ingest whole. The records it CREATED are untouched — they
-- are ordinary entities and entity obligations and belong to the tenant — and
-- they stay matchable by a later re-import, because the natural key lives in
-- the records themselves (an entity's name, an obligation's entity + type)
-- rather than in anything dropped here. What is lost is the history: which file
-- brought which records in, who committed it and when.

BEGIN;

DROP TRIGGER IF EXISTS import_batch_state_only ON import_batches;
DROP TRIGGER IF EXISTS import_row_immutable ON import_rows;
DROP FUNCTION IF EXISTS import_batch_state_only();
DROP FUNCTION IF EXISTS import_row_immutable();

DROP TABLE IF EXISTS import_rows;
DROP TABLE IF EXISTS import_batches;

COMMIT;
