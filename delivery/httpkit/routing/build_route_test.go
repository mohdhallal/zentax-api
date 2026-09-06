package routing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	gochi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// stubRoute is a route with no auth, no tenant and no transaction — the shape of
// the public /auth endpoints, which is exactly what the origin check used to
// miss (it lived inside RequireAuth, wired only for Tenant routes).
type stubRoute struct {
	def types.RouteDefinition
}

func (s stubRoute) DefineRoute() types.RouteDefinition { return s.def }

func (s stubRoute) Execute(
	http.ResponseWriter, *http.Request, *types.ValidatedInput, *app.Requester,
) (*types.HttpResponse, error) {
	return &types.HttpResponse{Status: http.StatusOK, Data: map[string]string{"reached": "yes"}}, nil
}

func mountStub(mode types.ServerMode, exposure types.Exposure, method, path string) *gochi.Mux {
	mux := gochi.NewRouter()
	router := NewRouter(mux, mode, nil, nil, "zentax_session", nil, nil)
	RegisterRoute(router, stubRoute{def: types.RouteDefinition{
		Method:   method,
		Path:     path,
		Exposure: exposure,
	}})
	return mux
}

func callWithOrigin(mux *gochi.Mux, method, target, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, http.NoBody)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// The builder — not the handler, and not RequireAuth — is where the guard is
// wired, so a route that declares nothing at all is still covered.
func TestBuildRoute_ExternalMutation_ForeignOriginRejected(t *testing.T) {
	t.Parallel()

	mux := mountStub(types.ModeExternal, types.Exposures.External, http.MethodPost, "/login")

	rr := callWithOrigin(mux, http.MethodPost, "https://api.zentax.test/login", "https://evil.example")

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.NotContains(t, rr.Body.String(), "reached")
}

func TestBuildRoute_ExternalMutation_SameOriginAndNoOriginAllowed(t *testing.T) {
	t.Parallel()

	mux := mountStub(types.ModeExternal, types.Exposures.External, http.MethodPost, "/login")

	same := callWithOrigin(mux, http.MethodPost, "https://api.zentax.test/login", "https://api.zentax.test")
	assert.Equal(t, http.StatusOK, same.Code)

	none := callWithOrigin(mux, http.MethodPost, "https://api.zentax.test/login", "")
	assert.Equal(t, http.StatusOK, none.Code)
}

func TestBuildRoute_ExternalRead_ForeignOriginAllowed(t *testing.T) {
	t.Parallel()

	mux := mountStub(types.ModeExternal, types.Exposures.External, http.MethodGet, "/me")

	rr := callWithOrigin(mux, http.MethodGet, "https://api.zentax.test/me", "https://evil.example")

	assert.Equal(t, http.StatusOK, rr.Code)
}

// The internal router is not browser-reachable and carries no ambient cookie,
// so the guard must not be able to refuse a server-to-server caller that
// happens to send an Origin.
func TestBuildRoute_InternalMutation_ForeignOriginAllowed(t *testing.T) {
	t.Parallel()

	mux := mountStub(types.ModeInternal, types.Exposures.Internal, http.MethodPost, "/sync")

	rr := callWithOrigin(mux, http.MethodPost, "https://internal.zentax.test/sync", "https://elsewhere.example")

	assert.Equal(t, http.StatusOK, rr.Code)
}
