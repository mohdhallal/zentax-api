package entities

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/modules/entities/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.EntityUseCases) {
	router.Group("/entities", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateEntityHandler(uc))
		routing.RegisterRoute(r, handlers.NewListEntitiesHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetEntityByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewEntityPeriodsHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateEntityHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteEntityHandler(uc))
	})
}
