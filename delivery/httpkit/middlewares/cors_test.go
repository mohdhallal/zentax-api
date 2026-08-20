package middlewares_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
)

func corsConfig(origins []string) config.CORSConfig {
	return config.CORSConfig{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: false,
		MaxAgeSec:        300,
	}
}

func TestCORSMiddleware_AllowedOrigin_SetsHeader(t *testing.T) {
	t.Parallel()

	mw := middlewares.CORSMiddleware(corsConfig([]string{"https://example.com"}))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()

	mw(next).ServeHTTP(w, r)

	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_Wildcard_SetsHeader(t *testing.T) {
	t.Parallel()

	mw := middlewares.CORSMiddleware(corsConfig([]string{"*"}))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Origin", "https://any-origin.io")
	w := httptest.NewRecorder()

	mw(next).ServeHTTP(w, r)

	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_Preflight_Returns200(t *testing.T) {
	t.Parallel()

	mw := middlewares.CORSMiddleware(corsConfig([]string{"https://app.example.com"}))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/users", http.NoBody)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "Authorization")
	w := httptest.NewRecorder()

	mw(next).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"))
}

func TestCORSMiddleware_NoOrigin_PassesThrough(t *testing.T) {
	t.Parallel()

	mw := middlewares.CORSMiddleware(corsConfig([]string{"https://example.com"}))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	mw(next).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCORSMiddleware_MaxAge_Propagated(t *testing.T) {
	t.Parallel()

	cfg := corsConfig([]string{"https://example.com"})
	cfg.MaxAgeSec = 600
	mw := middlewares.CORSMiddleware(cfg)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/", http.NoBody)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()

	mw(next).ServeHTTP(w, r)

	assert.Contains(t, w.Header().Get("Access-Control-Max-Age"), "600")
}
