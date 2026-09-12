package routing

import (
	"net/http"
	"strings"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares/ratelimit"
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
	fullPath := fullRoutePath(router, config.Path)

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

	// The per-PRINCIPAL request budget sits between the transaction and
	// RequireAuth, and that position is the whole point: it must run AFTER
	// RequireAuth (it keys off the authenticated principal — keying an
	// authenticated route by address would put a whole corporate NAT, or one
	// user's several sessions, into a single budget) and BEFORE the transaction
	// (a shed request must not first borrow a pooled connection and open a
	// transaction, which is the resource the shedding is meant to protect).
	if config.Tenant {
		handler = ratelimit.ByPrincipal(fullPath)(handler)
	}

	// RequireAuth must wrap OUTSIDE Transaction so the credential-derived tenant
	// (session cookie for humans, bearer API token for service accounts) is
	// bound to the context before WithinTransaction opens the tx and sets the
	// GUCs (ADR-0011 + machine identity B1).
	if config.Tenant {
		handler = middlewares.RequireAuth(router.SessionAuth, router.SessionCookieName)(handler)
	}

	// The verification bound (argon2id is 64 MiB per in-flight verification, and
	// /auth/login pays that for an unknown address too) wraps OUTSIDE RequireAuth
	// and the transaction, so a shed request holds nothing at all. Which paths it
	// gates is configuration; every other route is a pass-through.
	if router.Mode == types.ModeExternal {
		handler = ratelimit.Verification(fullPath)(handler)
	}

	// CSRF (ADR-0011): every route of the EXTERNAL router is origin-checked here
	// — the guard decides which requests that actually means (state-changing
	// methods that are not bearer-authenticated). Wiring it at the builder
	// rather than inside RequireAuth is the whole point: RequireAuth runs only
	// for routes that declare a tenant, which left the public /auth mutations —
	// logout included, a cookie-authenticated state change — unguarded. It wraps
	// OUTSIDE RequireAuth, so a cross-origin mutation is refused before any
	// credential is read.
	//
	// The internal router is deliberately excluded: it is not browser-reachable
	// and authenticates with per-call Basic credentials rather than an ambient
	// cookie, so the check could only reject legitimate server-to-server callers
	// that happen to send an Origin.
	if router.Mode == types.ModeExternal {
		handler = middlewares.CrossOriginGuard()(handler)
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

	// The per-ADDRESS budget is the OUTERMOST middleware of an unauthenticated
	// external route: a shed request must cost as little as possible, so it is
	// answered before the origin check, before any body is parsed and long
	// before a credential is read. Routes that declare a tenant are covered by
	// the per-principal budget above instead.
	//
	// Wiring both here rather than in the handlers is deliberate, for the same
	// reason the CSRF guard moved here: a new route cannot forget a control it
	// never had to remember.
	if router.Mode == types.ModeExternal && !config.Tenant {
		handler = ratelimit.ByAddress(fullPath)(handler)
	}

	return handler.ServeHTTP
}

// fullRoutePath is the path a route is reachable at — the group prefix plus the
// route's own path ("/auth" + "/login"). It repeats Router.addMeta's
// construction because the two are computed at different moments for different
// consumers (the OpenAPI registry there, the rate-limit policy here) and neither
// file may reach into the other's.
func fullRoutePath(router *Router, path string) string {
	full := strings.TrimRight(router.prefix+"/"+strings.TrimLeft(path, "/"), "/")
	if full == "" {
		return "/"
	}
	return full
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
