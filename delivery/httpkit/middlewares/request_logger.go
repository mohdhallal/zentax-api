package middlewares

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mohamadhallal/zentax-api/logger"
)

func RequestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := logger.Log.WithContext(r.Context())

		rw := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		start := time.Now()
		defer func() {
			duration := time.Since(start)
			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routePattern == "" {
				routePattern = "unknown"
			}

			l.Info("HTTP request",
				logger.String("method", r.Method),
				logger.String("route", routePattern),
				logger.String("url", r.RequestURI),
				logger.String("userAgent", r.Header.Get("User-Agent")),
				logger.String("remoteAddress", r.RemoteAddr),
				logger.Int("status", rw.Status()),
				logger.Duration("duration", duration),
			)
		}()

		next.ServeHTTP(rw, r)
	})
}
