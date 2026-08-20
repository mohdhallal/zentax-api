package middlewares

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mohamadhallal/zentax-api/platform/metrics"
)

func MetricsMiddleware(recorder metrics.HTTPRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder.InFlightInc()

			rw := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			defer func() {
				recorder.InFlightDec()

				route := chi.RouteContext(r.Context()).RoutePattern()
				if route == "" {
					route = "unknown"
				}

				status := rw.Status()
				if status == 0 {
					status = http.StatusOK
				}

				recorder.ObserveDuration(
					r.Method,
					route,
					strconv.Itoa(status),
					time.Since(start),
				)
			}()

			next.ServeHTTP(rw, r)
		})
	}
}
