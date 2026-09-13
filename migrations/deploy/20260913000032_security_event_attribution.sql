-- Deploy security_event_attribution
BEGIN;

-- ATTRIBUTION, the two halves of "from where and by what credential" that the
-- stream shipped without.
--
-- 1. client_ip_source — HOW client_ip was arrived at.
--
-- The address alone is not evidence. In every deployment that matters the API
-- has no public surface (CloudFront -> ALB -> web -> api in a cell; the web
-- container -> api in the self-host stack), so the socket peer is a hop of our
-- own on every row, and a reader cannot tell that from a real caller. The
-- resolver now walks the configured trusted-proxy chain
-- (delivery/httpkit/clientaddr, the same one the rate limiter keys budgets on),
-- which produces exactly three kinds of answer, and they are worth different
-- amounts:
--
--   peer      — the socket peer, and the socket peer is not one of our proxies.
--               A TCP handshake completed with it; it cannot be forged.
--   forwarded — read from the forwarded header, believed because the socket
--               peer IS a configured trusted proxy. Worth what that proxy is
--               worth, and nothing more.
--   proxy     — the socket peer, which is one of our proxies, because the
--               chain named no client. THE TRAP: a real row, about our own
--               infrastructure, that reads exactly like a caller. Without this
--               value the only way to tell it apart is to know what
--               RATE_LIMIT_TRUSTED_PROXIES said on the day the row was written.
--
-- NULL is meaningful and is the honest answer for a row whose address is NULL,
-- and for the rows written before this column existed: those carry a socket
-- peer of unknown standing, which is precisely what "we do not know" means. The
-- column is VARCHAR + CHECK rather than TEXT for the reason every other column
-- on this table is: no caller string may ever reach an append-only store.
ALTER TABLE security_events
    ADD COLUMN client_ip_source VARCHAR(10)
        CHECK (client_ip_source IN ('peer', 'forwarded', 'proxy'));

COMMENT ON COLUMN security_events.client_ip_source IS
    'How client_ip was arrived at: peer (the socket peer, unforgeable), forwarded (a trusted proxy''s header), proxy (our own hop — the proxy named no client, so this is NOT the caller). NULL where the address is unknown or predates the column.';

-- 2. invite_token — the credential /auth/accept-invite spends.
--
-- The invite-acceptance route is the only PUBLIC, unauthenticated endpoint in
-- the product that ESTABLISHES a credential: it redeems a single-use "zti_"
-- token and sets the account's first password. It recorded nothing at all,
-- which left the birth of every human principal but the founding administrator
-- invisible to the stream, and somebody guessing invite tokens invisible with
-- it. Recording it needs a method value of its own — it is neither a password
-- (none was verified; one was created) nor an api_token (a different prefix, a
-- machine principal, and reusable rather than spent).
ALTER TABLE security_events DROP CONSTRAINT security_events_method_check;

ALTER TABLE security_events ADD CONSTRAINT security_events_method_check
    CHECK (method IN ('password', 'totp', 'session', 'api_token', 'invite_token'));

COMMIT;
