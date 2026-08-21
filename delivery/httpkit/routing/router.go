package routing

import (
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	authdomain "github.com/mohamadhallal/zentax-api/modules/auth/domain"
	identity "github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

type RouteMeta struct {
	Definition types.RouteDefinition
	Schema     *types.SchemaDefinition
	FullPath   string
}

type Router struct {
	Chi               gochi.Router
	Mode              types.ServerMode
	AuthValidator     authdomain.Validator
	SessionAuth       identity.SessionAuthenticator
	SessionCookieName string
	Db                database.ExecerPgTx
	prefix            string
	registry          *[]RouteMeta
}

func NewRouter(
	chi gochi.Router,
	mode types.ServerMode,
	validator authdomain.Validator,
	sessionAuth identity.SessionAuthenticator,
	sessionCookieName string,
	db database.ExecerPgTx,
) *Router {
	registry := make([]RouteMeta, 0)
	return &Router{
		Chi:               chi,
		Mode:              mode,
		AuthValidator:     validator,
		SessionAuth:       sessionAuth,
		SessionCookieName: sessionCookieName,
		Db:                db,
		registry:          &registry,
	}
}

func (r *Router) Routes() []RouteMeta {
	return *r.registry
}

func (r *Router) Group(pattern string, fn func(sub *Router)) {
	r.Chi.Route(pattern, func(chiSub gochi.Router) {
		sub := &Router{
			Chi:               chiSub,
			Mode:              r.Mode,
			AuthValidator:     r.AuthValidator,
			SessionAuth:       r.SessionAuth,
			SessionCookieName: r.SessionCookieName,
			Db:                r.Db,
			prefix:            r.prefix + pattern,
			registry:          r.registry,
		}
		fn(sub)
	})
}

func (r *Router) addMeta(route types.Route) {
	def := route.DefineRoute()
	fullPath := r.prefix + "/" + strings.TrimLeft(def.Path, "/")
	fullPath = strings.TrimRight(fullPath, "/")
	if fullPath == "" {
		fullPath = "/"
	}

	meta := RouteMeta{
		Definition: def,
		FullPath:   fullPath,
	}

	if sp, ok := route.(types.RouteSchemaDefinition); ok {
		schema := sp.DefineSchema()
		meta.Schema = &schema
	}

	*r.registry = append(*r.registry, meta)
}
