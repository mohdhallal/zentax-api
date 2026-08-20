package middlewares

import (
	"net/http"

	"github.com/go-chi/cors"

	"github.com/mohamadhallal/zentax-api/config"
)

func CORSMiddleware(cfg config.CORSConfig) func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   cfg.AllowedMethods,
		AllowedHeaders:   cfg.AllowedHeaders,
		ExposedHeaders:   cfg.ExposedHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAgeSec,
	})
}
