package obligationtypes

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.ObligationTypeUseCases) {
	router.Group("/obligation-types", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateObligationTypeHandler(uc))
		routing.RegisterRoute(r, handlers.NewListObligationTypesHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetObligationTypeByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateObligationTypeHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteObligationTypeHandler(uc))
	})
}
