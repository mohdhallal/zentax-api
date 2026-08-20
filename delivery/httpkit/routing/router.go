package routing

import (
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

type RouteMeta struct {
	Definition types.RouteDefinition
	Schema     *types.SchemaDefinition
	FullPath   string
}

type Router struct {
	Chi           gochi.Router
	Mode          types.ServerMode
	AuthValidator domain.Validator
	Db            database.ExecerPgTx
	prefix        string
	registry      *[]RouteMeta
}

func NewRouter(chi gochi.Router, mode types.ServerMode, validator domain.Validator, db database.ExecerPgTx) *Router {
	registry := make([]RouteMeta, 0)
	return &Router{Chi: chi, Mode: mode, AuthValidator: validator, Db: db, registry: &registry}
}

func (r *Router) Routes() []RouteMeta {
	return *r.registry
}

func (r *Router) Group(pattern string, fn func(sub *Router)) {
	r.Chi.Route(pattern, func(chiSub gochi.Router) {
		sub := &Router{
			Chi:           chiSub,
			Mode:          r.Mode,
			AuthValidator: r.AuthValidator,
			Db:            r.Db,
			prefix:        r.prefix + pattern,
			registry:      r.registry,
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
