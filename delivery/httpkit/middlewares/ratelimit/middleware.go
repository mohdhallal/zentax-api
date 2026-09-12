package ratelimit

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/logger"
)

// CodeTooManyRequests is the envelope code a shed request carries. httperr's
// status map has no 429 entry, so Shed sets the status explicitly rather than
// reaching into a package this change does not own.
const CodeTooManyRequests httperr.AppErrorCode = "TOO_MANY_REQUESTS"

// MsgTooManyRequests is deliberately uninformative. Telling a caller WHICH
// budget they hit — their address, their principal, or the verification bound —
// tells them how to shape the next attempt.
const MsgTooManyRequests = "Too many requests"

// Key prefixes, so one store can serve both budgets (and so a Redis
// implementation inherits the same keyspace).
const (
	keyAddress   = "addr:"
	keyPrincipal = "principal:"
)

type limiterKey struct{}

// Provide puts the application's Limiter in the request context. It is wired
// once, in the global chain (bootstrap), and is what makes the per-route
// middlewares below per-application rather than per-process: the acceptance
// suite stands up several apps in one process, and a package-level limiter
// would leak one test's policy into another's.
//
// When it is not wired — rate limiting switched off, or a router built directly
// by a unit test — every middleware below is a pass-through.
func Provide(l *Limiter) types.Middleware {
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), limiterKey{}, l)))
		})
	}
}

// FromContext returns the Limiter the global chain provided, or nil.
func FromContext(ctx context.Context) *Limiter {
	l, _ := ctx.Value(limiterKey{}).(*Limiter)
	return l
}

// ByAddress charges the anonymous budget for the client address. It is wired
// around the routes that authenticate no principal — everything under /auth
// that is reached without a session, above all — where the address is the only
// identity there is.
func ByAddress(path string) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := FromContext(r.Context())
			if l == nil || l.ExemptsPath(path) || !l.settings.Anonymous.Active() {
				next.ServeHTTP(w, r)
				return
			}
			addr, identified := l.resolver.ResolveClient(r)
			if !identified {
				// The key would be shared by everyone behind an unappending
				// proxy, so charging it would let one caller's burst refuse
				// login for the whole installation. Skip this budget, say so
				// once, and leave the per-account attempt budget and the
				// verification bound — which do not depend on the address — to
				// carry the route.
				l.warnSharedAddress(r, addr)
				next.ServeHTTP(w, r)
				return
			}
			l.charge(w, r, next, keyAddress+addr, l.settings.Anonymous)
		})
	}
}

// ByPrincipal charges the authenticated budget for the caller, keyed by the
// principal RequireAuth resolved — NOT by the address. Keying an authenticated
// route by address would put every user behind one corporate NAT, and every
// user of one browser profile with several sessions, into a single budget.
//
// It is wired INSIDE RequireAuth (the principal has to exist) and OUTSIDE the
// transaction middleware (a shed request must not open a database transaction
// first). A request that somehow arrives with no principal falls back to the
// address key, so the route is never left unlimited.
func ByPrincipal(path string) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := FromContext(r.Context())
			if l == nil || l.ExemptsPath(path) || !l.settings.Authenticated.Active() {
				next.ServeHTTP(w, r)
				return
			}
			l.charge(w, r, next, l.principalKey(r), l.settings.Authenticated)
		})
	}
}

// Verification holds a Gate slot for the duration of a handler that performs a
// password or token verification, so the 64 MiB-per-verification cost cannot be
// multiplied without limit. Which paths those are is configuration
// (rateLimit.verification.paths); every other route is a pass-through.
func Verification(path string) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := FromContext(r.Context())
			if l == nil || !l.GatesPath(path) {
				next.ServeHTTP(w, r)
				return
			}

			if err := l.gate.Acquire(r.Context()); err != nil {
				if !errors.Is(err, ErrOverloaded) {
					// The caller's context ended while queued — they are gone.
					// Shedding is still the truthful answer; nothing reads it.
					logger.Log.WithContext(r.Context()).Debug("verification wait abandoned",
						logger.String("path", path), logger.String("reason", err.Error()))
				}
				Shed(w, r, 0)
				return
			}
			defer l.gate.Release()

			next.ServeHTTP(w, r)
		})
	}
}

// charge takes one token and either runs the handler or sheds.
func (l *Limiter) charge(
	w http.ResponseWriter, r *http.Request, next http.Handler, key string, rule Rule,
) {
	decision, err := l.store.Take(r.Context(), key, rule)
	if err != nil {
		// FAIL OPEN, loudly. The store is a seam a shared (Redis) backend will
		// sit behind, and a limiter outage must not become an API outage. The
		// Gate is untouched by this: it is in-process, so the memory bound the
		// exhaustion lever actually needs still holds.
		logger.Log.WithContext(r.Context()).Warn("rate limit store unavailable; allowing request",
			logger.String("path", r.URL.Path), logger.String("error", err.Error()))
		next.ServeHTTP(w, r)
		return
	}
	if !decision.Allowed {
		Shed(w, r, decision.RetryAfter)
		return
	}
	next.ServeHTTP(w, r)
}

// principalKey is the authenticated caller's key, falling back to the address.
// The fallback is only reached when a route somehow carries no principal; an
// unidentifiable address there is still better than no limit at all, because an
// authenticated route cannot be reached by an anonymous flood.
func (l *Limiter) principalKey(r *http.Request) string {
	if requester := app.GetRequester(r.Context()); requester != nil && requester.ID != "" {
		return keyPrincipal + requester.ID
	}
	return keyAddress + l.resolver.ClientAddress(r)
}

// warnSharedAddress tells the operator, once per process, that the proxy in
// front of the API is not appending the client address, so the per-client
// budget on the unauthenticated routes is not in effect. Once, because this is
// a deployment fact rather than a per-request event, and a line per login
// attempt would be its own denial of service against the log.
func (l *Limiter) warnSharedAddress(r *http.Request, addr string) {
	l.sharedAddressOnce.Do(func() {
		logger.Log.WithContext(r.Context()).Warn(
			"rate limit: the per-client budget on unauthenticated routes is not in effect",
			logger.String("reason", "the socket peer is a trusted proxy and it forwarded no client address"),
			logger.String("peer", addr),
			logger.String("forwardedHeader", l.resolver.Header()),
			logger.String("fix", "make the proxy append the client address, or stop trusting it via RATE_LIMIT_TRUSTED_PROXIES"),
		)
	})
}

// Shed answers 429 with a Retry-After header and the standard error envelope.
//
// It goes through httperr.HandleError, which logs a shed at WARN and only
// reserves ERROR (with a stack trace) for the INTERNAL code — a limiter doing
// its job must not read as the service failing, or the first real burst buries
// the error budget and the on-call pager under its own defence.
func Shed(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))

	appErr := httperr.New(CodeTooManyRequests, MsgTooManyRequests)
	// httperr's status map carries no 429, so New defaulted this to 500. A shed
	// request is a client answer, not a server failure.
	appErr.Status = http.StatusTooManyRequests

	httperr.HandleError(w, r, appErr)
}

// retryAfterSeconds rounds a wait up to whole seconds, with a floor of 1:
// Retry-After takes an integer, and "0" reads as "retry immediately", which is
// the opposite of what a shed request should be told.
func retryAfterSeconds(d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}
