package routing

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func RegisterRoute(router *Router, route types.Route) {
	cfg := route.DefineRoute()
	if !shouldMount(cfg.Exposure, router.Mode) {
		return
	}

	router.addMeta(route)

	handler := buildRoute(route, router)
	path := cfg.Path

	switch cfg.Method {
	case http.MethodGet:
		router.Chi.Get(path, handler)
	case http.MethodPost:
		router.Chi.Post(path, handler)
	case http.MethodPut:
		router.Chi.Put(path, handler)
	case http.MethodPatch:
		router.Chi.Patch(path, handler)
	case http.MethodDelete:
		router.Chi.Delete(path, handler)
	}
}

func buildRoute(route types.Route, router *Router) http.HandlerFunc {
	config := route.DefineRoute()

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		input := &types.ValidatedInput{
			Query:  extractQuery(r),
			Params: extractParams(r),
		}

		if err := validateInput(r, route, input); err != nil {
			httperr.HandleError(w, r, err)
			return
		}

		if config.Paginated {
			var sortColMap map[string]string
			if pd, ok := route.(types.RoutePaginationDefinition); ok {
				sortColMap = pd.DefineSortColumns()
			}
			input.Pagination = extractPagination(input.Query, sortColMap)
		}

		requester := app.GetRequester(r.Context())

		response, err := route.Execute(w, r, input, requester)
		if err != nil {
			httperr.HandleError(w, r, err)
			return
		}

		writeResponse(w, r, response)
	})

	// The capability check (scoped RBAC, ADR-0012) must run INSIDE the transaction
	// so its RLS-scoped user_grants read sees the session tenant, but BEFORE the
	// handler — so it wraps the handler here and Transaction wraps it just below.
	if config.Capability != "" {
		handler = middlewares.RequireCapability(router.Grants, config.Capability)(handler)
	}

	// A tenant-scoped route needs a transaction to carry the SET LOCAL
	// app.tenant_id GUC that drives RLS (ADR-0004), so Tenant implies Tx. A
	// streaming (download) handler writes through instead of being buffered.
	if config.Tx || config.Tenant {
		if config.Stream {
			handler = middlewares.StreamingTransaction(router.Db)(handler)
		} else {
			handler = middlewares.Transaction(router.Db)(handler)
		}
	}

	// RequireAuth must wrap OUTSIDE Transaction so the credential-derived tenant
	// (session cookie for humans, bearer API token for service accounts) is
	// bound to the context before WithinTransaction opens the tx and sets the
	// GUCs (ADR-0011 + machine identity B1).
	if config.Tenant {
		handler = middlewares.RequireAuth(router.SessionAuth, router.SessionCookieName)(handler)
	}

	if mp, ok := route.(types.RouteMiddlewareDefinition); ok {
		for i := len(mp.DefineMiddlewares()) - 1; i >= 0; i-- {
			handler = mp.DefineMiddlewares()[i](handler)
		}
	}

	if config.Auth {
		if config.Exposure == types.Exposures.Internal {
			handler = middlewares.RequireInternalAuth(router.AuthValidator)(handler)
		} else {
			handler = middlewares.RequireExternalAuth()(handler)
		}
	}

	return handler.ServeHTTP
}

func validateInput(
	r *http.Request, route types.Route, input *types.ValidatedInput,
) error {
	sp, ok := route.(types.RouteSchemaDefinition)
	if !ok {
		return nil
	}

	schema := sp.DefineSchema()

	if schema.Body != nil {
		raw := parseAndSanitizeBody(r)
		validated, err := validateBody(raw, schema.Body)
		if err != nil {
			return httperr.New(httperr.ErrValidation, "INVALID_BODY", err.Error())
		}
		input.Body = validated
	}

	if schema.Query != nil {
		queryMap, _ := input.Query.(map[string][]string)
		validated, err := validateQuery(queryMap, schema.Query)
		if err != nil {
			return httperr.New(httperr.ErrValidation, "INVALID_QUERY", err.Error())
		}
		input.Query = validated
	}

	if schema.Params != nil {
		paramsMap, _ := input.Params.(map[string]string)
		validated, err := validateParams(paramsMap, schema.Params)
		if err != nil {
			return httperr.New(httperr.ErrValidation, "INVALID_PARAMS", err.Error())
		}
		input.Params = validated
	}

	return nil
}

func shouldMount(exposure types.Exposure, mode types.ServerMode) bool {
	if exposure == types.Exposures.Both {
		return true
	}
	if exposure == types.Exposures.External && mode == types.ModeExternal {
		return true
	}
	if exposure == types.Exposures.Internal && mode == types.ModeInternal {
		return true
	}
	return false
}
