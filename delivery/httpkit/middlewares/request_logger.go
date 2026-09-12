package middlewares

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mohamadhallal/zentax-api/logger"
)

// maxUserAgentRunes bounds the one field on this line whose content the caller
// chooses. A User-Agent is worth keeping — it is how a misbehaving client
// version is found — but it is free text, and crawlers routinely put a contact
// address in it, so it is clipped and scrubbed rather than trusted.
const maxUserAgentRunes = 200

// RequestLoggerMiddleware writes one line per request.
//
// ADR-0015, and why this line looks the way it does. A log sits outside the
// erasure boundary, so what it records cannot later be deleted on request.
// This middleware used to emit r.RequestURI verbatim, and every list endpoint
// takes a `search` parameter — so `GET /members?search=jane@acme.test` wrote
// that address into an unerasable store, next to the caller's network address,
// on every request. What it emits now:
//
//   - route — the chi pattern ("/members/{id}"). Ours by construction, and the
//     right key to group on.
//   - path — the actual path, segment by segment, anything not shaped like a
//     literal or an id replaced (logger.RedactPath). A matched route comes
//     through unchanged; a 404 for "/jane@acme.test" does not.
//   - query — the query KEYS with every value replaced ("search=[redacted]").
//     That a member search ran is operationally useful; the term is not.
//   - no remoteAddress. Dropped deliberately, not overlooked. An IP address is
//     personal data, and this one is the WRONG address anyway: behind the ALB
//     (every deployed topology, ADR-0024) and behind the Compose proxy,
//     r.RemoteAddr is the load balancer's or the web container's ENI, not the
//     caller's — the audit's own capture reads `remoteAddress=172.18.0.4:53520`,
//     which is the web tier. So the field cost unerasable personal data exactly
//     when the API is reached directly, and told the operator nothing when it
//     is not. Where a client address genuinely IS the signal — brute force and
//     credential stuffing on the unauthenticated routes — it is resolved
//     spoof-resistantly by ratelimit.AddressResolver, and the ALB access log
//     (S3, its own lifecycle) is the artefact for network-level forensics.
func RequestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := logger.Log.WithContext(r.Context())

		rw := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		start := time.Now()
		defer func() {
			duration := time.Since(start)
			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routePattern == "" {
				routePattern = "unknown"
			}

			fields := []logger.Field{
				logger.String("method", r.Method),
				logger.String("route", routePattern),
				logger.String("path", logger.RedactPath(r.URL.Path)),
			}
			if query := logger.RedactQuery(r.URL.RawQuery); query != "" {
				fields = append(fields, logger.String("query", query))
			}
			fields = append(fields,
				logger.String("userAgent", logger.RedactText(r.Header.Get("User-Agent"), maxUserAgentRunes)),
				logger.Int("status", rw.Status()),
				logger.Duration("duration", duration),
			)

			l.Info("HTTP request", fields...)
		}()

		next.ServeHTTP(rw, r)
	})
}
