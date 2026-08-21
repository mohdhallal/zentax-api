package middlewares

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// RequireCapability enforces that the authenticated requester holds cap through
// one of their granted roles (scoped RBAC, ADR-0012). It must run INSIDE the
// tenant transaction so the RLS-scoped user_grants read sees the right tenant;
// build_route places it after RequireSession + Transaction. The loaded grants
// are stashed on the context for downstream per-entity scope checks.
//
// This is the tenant-wide capability gate: it answers "may this user do X in
// this tenant at all". Narrowing a scoped grant to its entity subtree is a
// separate, per-resource check (not yet wired) — see authz package docs.
func RequireCapability(loader authz.GrantLoader, cap authz.Capability) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requester := app.GetRequester(r.Context())
			if requester == nil || !requester.IsUser() || requester.ID == "" {
				httperr.HandleError(w, r, httperr.New(httperr.ErrForbidden, "authorization required"))
				return
			}

			// Human-only capabilities (ADR-0012/0018): approval is an
			// attestation — a service principal may never exercise it,
			// regardless of its granted roles.
			if requester.ServiceAccount && authz.HumanOnly(cap) {
				httperr.HandleError(w, r, httperr.New(httperr.ErrForbidden, "this action requires a human user"))
				return
			}

			grants, err := loader.ListForUser(r.Context(), requester.ID)
			if err != nil {
				httperr.HandleError(w, r, err)
				return
			}

			if !authz.HasCapability(grants, cap) {
				httperr.HandleError(w, r, httperr.New(httperr.ErrForbidden, "insufficient permissions"))
				return
			}

			ctx := authz.WithGrants(r.Context(), grants)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
