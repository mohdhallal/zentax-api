package middlewares

import (
	"net/http"
	"net/url"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// CrossOriginGuard refuses a state-changing request that a browser made from
// another origin (CSRF). It is wired ONCE, in the route builder, around every
// route of the EXTERNAL router — so a new route cannot forget it.
//
// It used to live inside RequireAuth, which the builder wires only for routes
// that declare a tenant. The public /auth routes declare none, so nothing
// checked them: a page on any origin could POST /auth/logout with the victim's
// cookie and genuinely destroy their session, or POST /auth/login to fixate one.
//
// The decision table:
//
//	GET / HEAD / OPTIONS          → allowed: nothing changes state.
//	Authorization: Bearer ztx_... → allowed: an API token is not ambient
//	  authority — a browser never attaches it on behalf of a foreign page — so
//	  CSRF cannot be mounted with it, and service-account clients legitimately
//	  call from another origin. (Any other Authorization value falls through to
//	  cookie auth and is checked like a cookie request.)
//	no Origin header              → allowed: server-to-server clients, curl and
//	  the Express adapter omit it; a browser always sends it on a cross-site
//	  state-changing request.
//	Origin host == request host   → allowed.
//	anything else                 → 403.
//
// It runs before authentication, so a cross-origin mutation is refused without
// the request ever touching a credential. With the SameSite=Strict session
// cookie this is defence in depth. A configurable origin allowlist (for a
// browser app served from a different host than the API) can replace the host
// comparison later; today the Express adapter strips Origin, so the API only
// ever sees one from a direct browser call.
func CrossOriginGuard() types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutation(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if _, isAPIToken := bearerToken(r); isAPIToken {
				next.ServeHTTP(w, r)
				return
			}
			if !sameOrigin(r) {
				httperr.HandleError(w, r, httperr.New(httperr.ErrForbidden, "cross-origin request rejected"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOrigin reports whether the request's Origin names the host it was sent
// to. A request without an Origin is treated as same-origin (see the table
// above); an unparseable one never is.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
