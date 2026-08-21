package types

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type Middleware = func(http.Handler) http.Handler

type RouteDefinition struct {
	Method    string
	Path      string
	Exposure  Exposure
	Auth      bool
	Tenant    bool // tenant-scoped: resolve the tenant and bind it for RLS (ADR-0004); implies Tx
	Tx        bool
	Paginated bool
	// Capability, if set, requires the authenticated user to hold it through a
	// granted role before the handler runs (scoped RBAC, ADR-0012). Enforced by
	// RequireCapability, which runs inside the tenant transaction. Only meaningful
	// with Tenant: true (it needs the session + tenant + tx).
	Capability authz.Capability
}

type SchemaDefinition struct {
	Body   any
	Query  any
	Params any
}

type ValidatedInput struct {
	Body       any
	Query      any
	Params     any
	Pagination *sharedtypes.ListArgs
}

type Route interface {
	DefineRoute() RouteDefinition
	Execute(w http.ResponseWriter, r *http.Request, input *ValidatedInput, requester *app.Requester) (*HttpResponse, error)
}

type RouteSchemaDefinition interface {
	DefineSchema() SchemaDefinition
}

type RouteMiddlewareDefinition interface {
	DefineMiddlewares() []Middleware
}

// RoutePaginationDefinition is implemented by paginated list routes to declare
// which camelCase sort keys map to DB column names.
type RoutePaginationDefinition interface {
	DefineSortColumns() map[string]string
}
