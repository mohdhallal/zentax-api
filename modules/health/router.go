package health

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/health/handlers"
)

// RegisterRoutes mounts the probes:
//
//	GET /health        liveness  — the process answers; no dependencies
//	GET /health/ready  readiness — the database answers SELECT 1 within 2 s (else 503)
//
// Both are unauthenticated and mounted on the external and internal listeners.
func RegisterRoutes(router *routing.Router, mode types.ServerMode, db handlers.Pinger) {
	router.Group("/health", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCheckHandler(mode))
		routing.RegisterRoute(r, handlers.NewReadyHandler(db))
	})
}
