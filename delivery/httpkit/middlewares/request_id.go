package middlewares

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
)

// RequestIdMiddleware binds the request's correlation id, honouring an
// X-Request-Id header from the caller ONLY when it is a canonical UUID.
//
// The header is the one caller-supplied value that reaches the audit envelope:
// it is written into audit_log.request_id, folded into the per-tenant hash
// chain, and exported into WORM segments that a compliance-locked bucket keeps
// for ten years with no delete verb for any principal. Honouring whatever
// arrived would let any authenticated caller — or anybody who can reach the API
// port directly, which the self-host edition publishes — write arbitrary bytes
// into that store, against the ADR-0008 rule that the envelope carries no free
// text, and with no way to take it back. So a header of any other shape costs
// the caller its correlation and nothing else: a fresh id is minted, exactly as
// if the header had been absent.
//
// A caller's own UUID is still honoured (lowercased, so it joins the security
// stream's UUID column), because tracing a request across the tiers is why the
// header exists.
func RequestIdMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := app.NormalizeRequestID(r.Header.Get("X-Request-Id"))
		if !ok {
			id = app.NewRequestID()
		}
		ctx := app.WithRequestId(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
