package swagger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gochi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func mountedMux(t *testing.T) *gochi.Mux {
	t.Helper()
	mux := gochi.NewMux()
	Mount(mux, []routing.RouteMeta{}, types.ModeExternal, Config{Title: "Test API", Version: "1.0.0"})
	return mux
}

func TestMount_SpecJSON_ContentType(t *testing.T) {
	t.Parallel()

	mux := mountedMux(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/spec.json", http.NoBody)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestMount_SpecJSON_ValidJSON(t *testing.T) {
	t.Parallel()

	mux := mountedMux(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/spec.json", http.NoBody)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, r)

	var spec map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &spec))
	assert.Equal(t, "3.0.3", spec["openapi"])
}

func TestMount_SwaggerUI_ContentType(t *testing.T) {
	t.Parallel()

	mux := mountedMux(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger", http.NoBody)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestMount_SwaggerUI_ContainsTitle(t *testing.T) {
	t.Parallel()

	mux := mountedMux(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger", http.NoBody)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, r)

	assert.Contains(t, w.Body.String(), "Test API")
	assert.Contains(t, w.Body.String(), "swagger-ui")
}

func TestMount_SwaggerUI_EmptyTitle_DefaultsToAPI(t *testing.T) {
	t.Parallel()

	mux := gochi.NewMux()
	Mount(mux, []routing.RouteMeta{}, types.ModeExternal, Config{})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	assert.Contains(t, w.Body.String(), "API")
}

func TestSwaggerUIHTML_ContainsSpecURL(t *testing.T) {
	t.Parallel()

	html := swaggerUIHTML("My API")
	assert.Contains(t, html, "/swagger/spec.json")
	assert.Contains(t, html, "My API")
}
