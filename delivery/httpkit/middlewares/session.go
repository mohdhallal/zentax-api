package middlewares

import (
	"net/http"
	"net/url"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	identity "github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// RequireSession authenticates the request from the session cookie and binds the
// user + tenant to the context (ADR-0011). It replaces the interim X-Tenant-ID
// header: the tenant now comes from the authenticated session, and the Tx seam
// reads it exactly as before. mfa_pending sessions are rejected (MFA must be
// completed first). Cross-origin state-changing requests are rejected (CSRF).
func RequireSession(auth identity.SessionAuthenticator, cookieName string) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMutation(r.Method) && !sameOrigin(r) {
				httperr.HandleError(w, r, httperr.New(httperr.ErrForbidden, "cross-origin request rejected"))
				return
			}

			session, user, err := auth.Authenticate(r.Context(), sessionToken(r, cookieName))
			if err != nil {
				httperr.HandleError(w, r, err)
				return
			}
			if session.MFAPending {
				httperr.HandleError(w, r, httperr.New(httperr.ErrUnauthorized, identity.MsgMFARequired))
				return
			}

			ctx := app.WithRequester(r.Context(), &app.Requester{Kind: app.RequesterUser, ID: user.ID})
			ctx = app.WithTenantID(ctx, session.TenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func sessionToken(r *http.Request, cookieName string) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOrigin is a lightweight CSRF check: a cross-origin browser request carries
// an Origin whose host differs from the request host. Non-browser clients that
// omit Origin are allowed (combined with a SameSite=Strict cookie). A configurable
// allowlist can replace this later.
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
