package middlewares

import (
	"net/http"
	"strings"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	identity "github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// RequireAuth authenticates the request and binds the principal + tenant to the
// context. Two credential forms (ADR-0011 + machine identity B1):
//
//   - Authorization: Bearer ztx_... — an API token resolving to a SERVICE
//     ACCOUNT (Requester.ServiceAccount = true). MFA does not apply.
//   - Session cookie — a human user. mfa_pending sessions are rejected (MFA
//     must be completed first).
//
// Either way the tenant comes from the authenticated credential, and the Tx
// seam binds it (with app.user_id) for RLS + attribution, unchanged.
//
// CSRF is NOT decided here: the origin check is CrossOriginGuard, wired by the
// route builder around every external route — including the public /auth
// mutations, which never reach this middleware because they declare no tenant.
func RequireAuth(auth identity.RequestAuthenticator, cookieName string) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw, ok := bearerToken(r); ok {
				token, user, err := auth.AuthenticateToken(r.Context(), raw)
				if err != nil {
					httperr.HandleError(w, r, err)
					return
				}
				ctx := app.WithRequester(r.Context(), &app.Requester{
					Kind: app.RequesterUser, ID: user.ID, ServiceAccount: true,
				})
				ctx = app.WithTenantID(ctx, token.TenantID)
				next.ServeHTTP(w, r.WithContext(ctx))
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

// bearerToken extracts a ZenTax API token from the Authorization header. Only
// "Bearer ztx_..." values are treated as API tokens; anything else falls
// through to session auth.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const scheme = "Bearer "
	if !strings.HasPrefix(h, scheme) {
		return "", false
	}
	raw := strings.TrimSpace(h[len(scheme):])
	if !strings.HasPrefix(raw, identity.TokenPrefix) {
		return "", false
	}
	return raw, true
}

func sessionToken(r *http.Request, cookieName string) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
