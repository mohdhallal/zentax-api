-- Verify audit_request_id_shape

DO $$
DECLARE
    expr TEXT;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO expr
    FROM pg_constraint
    WHERE conrelid = 'audit_log'::regclass
      AND conname = 'audit_log_request_id_shape';

    IF expr IS NULL THEN
        RAISE EXCEPTION 'audit_log_request_id_shape is missing: X-Request-Id would again be a door into an append-only store';
    END IF;

    -- The shape must be the canonical LOWERCASE uuid: a case-insensitive
    -- constraint would admit a second spelling of one id, which joins the
    -- security stream's UUID column to nothing.
    IF expr !~ '0-9a-f' OR expr ~ 'A-F' THEN
        RAISE EXCEPTION 'audit_log_request_id_shape does not constrain request_id to a lowercase canonical UUID: %', expr;
    END IF;
END
$$;
