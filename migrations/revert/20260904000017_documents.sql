-- Revert documents
BEGIN;

-- Dropping the partitioned parents drops every hash partition with them.
DROP TABLE IF EXISTS document_versions;
DROP TABLE IF EXISTS documents;

COMMIT;
