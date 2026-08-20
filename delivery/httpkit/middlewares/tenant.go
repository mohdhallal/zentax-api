package middlewares

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// tenantHeader carries the caller's tenant in the interim, header-based
// tenancy model. TODO(ADR-0011): once session auth lands, derive the tenant
// from the authenticated session (and verify the user's membership) instead
// of trusting a client-supplied header.
const tenantHeader = "X-Tenant-ID"

// RequireTenant resolves the caller's tenant and binds it to the request
// context so the Tx seam (database.Exec.WithinTransaction) can set the
// `app.tenant_id` Postgres GUC that drives RLS isolation (ADR-0004). It fails
// closed with 401 when the tenant is absent or not a valid UUID.
func RequireTenant() types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(r.Header.Get(tenantHeader))
			if err != nil {
				httperr.HandleError(w, r, httperr.New(
					httperr.ErrUnauthorized,
					"a valid "+tenantHeader+" header is required",
				))
				return
			}

			ctx := app.WithTenantID(r.Context(), id.String())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
