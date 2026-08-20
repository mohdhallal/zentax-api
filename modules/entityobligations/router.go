package entityobligations

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.EntityObligationUseCases) {
	router.Group("/entity-obligations", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateEntityObligationHandler(uc))
		routing.RegisterRoute(r, handlers.NewListEntityObligationsHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetEntityObligationByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateEntityObligationHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteEntityObligationHandler(uc))
	})
}
