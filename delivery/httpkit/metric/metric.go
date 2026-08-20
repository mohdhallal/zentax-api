package metric

import (
	"net/http"

	gochi "github.com/go-chi/chi/v5"
)

func Mount(mux *gochi.Mux, handler http.Handler) {
	mux.Method("GET", "/metrics", handler)
}
