package routing

import (
	"net/http"
	"testing"

	gochi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func TestCapitalize(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"users", "Users"},
		{"Users", "Users"},
		{"", ""},
		{"a", "A"},
		{"ABC", "ABC"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, capitalize(tc.in))
		})
	}
}

func TestPrintRoutes_NoPanic(t *testing.T) {
	t.Parallel()

	chi := gochi.NewRouter()
	chi.Get("/users", func(w http.ResponseWriter, r *http.Request) {})
	chi.Post("/users", func(w http.ResponseWriter, r *http.Request) {})
	chi.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {})
	chi.Get("/health", func(w http.ResponseWriter, r *http.Request) {})

	assert.NotPanics(t, func() {
		PrintRoutes(chi, types.ModeExternal)
	})
}

func TestPrintRoutes_EmptyRouter_NoPanic(t *testing.T) {
	t.Parallel()

	chi := gochi.NewRouter()
	assert.NotPanics(t, func() {
		PrintRoutes(chi, types.ModeInternal)
	})
}

func TestPrintRoutes_GroupedRoutes_NoPanic(t *testing.T) {
	t.Parallel()

	chi := gochi.NewRouter()
	chi.Route("/v1", func(r gochi.Router) {
		r.Get("/orders", func(w http.ResponseWriter, r *http.Request) {})
		r.Post("/orders", func(w http.ResponseWriter, r *http.Request) {})
		r.Get("/payments/{id}", func(w http.ResponseWriter, r *http.Request) {})
	})

	assert.NotPanics(t, func() {
		PrintRoutes(chi, types.ModeExternal)
	})
}
