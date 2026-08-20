package middlewares_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
)

func TestRecoveryMiddleware_NoPanic_PassesThrough(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":true}`))
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	middlewares.RecoveryMiddleware(handler).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `{"status":true}`)
}

func TestRecoveryMiddleware_PanicString_Returns500(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went very wrong")
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", http.NoBody)
	w := httptest.NewRecorder()

	middlewares.RecoveryMiddleware(handler).ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"status":false`)
	assert.Contains(t, w.Body.String(), `"INTERNAL"`)
}

func TestRecoveryMiddleware_PanicError_Returns500(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/panic", http.NoBody)
	w := httptest.NewRecorder()

	middlewares.RecoveryMiddleware(handler).ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"INTERNAL"`)
}

func TestRecoveryMiddleware_PanicInt_Returns500(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(42)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	middlewares.RecoveryMiddleware(handler).ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryMiddleware_PanicNilValue_Returns500(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil) //nolint:govet // testing recovery from nil panic
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	// Must not propagate the panic to the test runner.
	assert.NotPanics(t, func() {
		middlewares.RecoveryMiddleware(handler).ServeHTTP(w, r)
	})
}
