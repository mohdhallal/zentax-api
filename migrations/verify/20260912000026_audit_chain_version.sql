-- Verify audit_chain_version
SELECT hash_version
FROM audit_log
WHERE false;

-- Every row that existed before the cut-over must be marked unverifiable, and
-- the default must stay 1 so an unaware writer is recorded the same way.
DO $$
BEGIN
    IF (SELECT column_default FROM information_schema.columns
        WHERE table_name = 'audit_log' AND column_name = 'hash_version') <> '1' THEN
        RAISE EXCEPTION 'audit_log.hash_version must default to 1 (pre-cut-over)';
    END IF;
END
$$;
