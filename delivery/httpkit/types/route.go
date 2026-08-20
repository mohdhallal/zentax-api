package types

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type Middleware = func(http.Handler) http.Handler

type RouteDefinition struct {
	Method    string
	Path      string
	Exposure  Exposure
	Auth      bool
	Tx        bool
	Paginated bool
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
