-- Revert invite_tokens
BEGIN;

-- Dropping the partitioned parent drops every partition with it.
DROP TABLE IF EXISTS invite_tokens;

COMMIT;
