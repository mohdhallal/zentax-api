-- Verify audit_credential

SELECT credential_id, credential_kind
FROM audit_log
WHERE false;

DO $$
BEGIN
    -- The pair must be typed, not text: a UUID column is what keeps a join key
    -- from becoming a place to write things.
    IF (SELECT data_type FROM information_schema.columns
        WHERE table_name = 'audit_log' AND column_name = 'credential_id') <> 'uuid' THEN
        RAISE EXCEPTION 'audit_log.credential_id must be UUID';
    END IF;

    -- Half a credential is a join key pointing at no table.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'audit_log'::regclass
                     AND conname = 'audit_log_credential_pair') THEN
        RAISE EXCEPTION 'audit_log_credential_pair is missing';
    END IF;

    -- The kind is a closed vocabulary, never free text.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'audit_log'::regclass
                     AND conname = 'audit_log_credential_kind') THEN
        RAISE EXCEPTION 'audit_log_credential_kind is missing';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'idx_audit_log_credential') THEN
        RAISE EXCEPTION 'idx_audit_log_credential is missing (the incident-review query would seq-scan the ledger)';
    END IF;
END
$$;
