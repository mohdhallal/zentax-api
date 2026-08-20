package health

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/health/handlers"
)

func RegisterRoutes(router *routing.Router, mode types.ServerMode) {
	router.Group("/health", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCheckHandler(mode))
	})
}
