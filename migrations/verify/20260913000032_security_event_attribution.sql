-- Verify security_event_attribution

-- The provenance column exists, is nullable (a row may legitimately not know
-- where it came from) and is not free text.
SELECT client_ip_source FROM security_events WHERE false;

SELECT 1 / (
    (SELECT NOT attnotnull FROM pg_attribute
      WHERE attrelid = 'security_events'::regclass AND attname = 'client_ip_source')
    AND (SELECT format_type(atttypid, atttypmod) = 'character varying(10)'
           FROM pg_attribute
          WHERE attrelid = 'security_events'::regclass AND attname = 'client_ip_source')
)::int;

-- Its vocabulary is closed: the three answers the resolver can give, and
-- nothing else. 'proxy' is the one that must be present — it is what tells a
-- reader that the address is our own load balancer rather than a caller.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_constraint
             WHERE conrelid = 'security_events'::regclass
               AND contype = 'c'
               AND pg_get_constraintdef(oid) LIKE '%client_ip_source%'
               AND pg_get_constraintdef(oid) LIKE '%peer%'
               AND pg_get_constraintdef(oid) LIKE '%forwarded%'
               AND pg_get_constraintdef(oid) LIKE '%proxy%')
)::int;

-- The invite credential is a method of its own, and the method vocabulary is
-- still closed.
SELECT 1 / (
    EXISTS (SELECT 1 FROM pg_constraint
             WHERE conrelid = 'security_events'::regclass
               AND conname = 'security_events_method_check'
               AND pg_get_constraintdef(oid) LIKE '%invite_token%')
)::int;
