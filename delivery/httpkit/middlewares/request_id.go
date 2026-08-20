package middlewares

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
)

func generateRequestId() string {
	return uuid.NewString()
}

func RequestIdMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateRequestId()
		}
		ctx := app.WithRequestId(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
