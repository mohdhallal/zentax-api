-- Revert security_event_attribution
BEGIN;

-- The narrowed CHECK goes back first: dropping the column cannot undo rows
-- already written with the new method, so a revert that left invite_token rows
-- behind would fail on the narrower constraint. There are none to salvage —
-- reverting this change also reverts the code that writes them — and the
-- constraint is validated on the way in, which is the check that says so.
ALTER TABLE security_events DROP CONSTRAINT security_events_method_check;

ALTER TABLE security_events ADD CONSTRAINT security_events_method_check
    CHECK (method IN ('password', 'totp', 'session', 'api_token'));

ALTER TABLE security_events DROP COLUMN client_ip_source;

COMMIT;
