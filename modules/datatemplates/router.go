package datatemplates

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.DataTemplateUseCases) {
	router.Group("/data-templates", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewListDataTemplatesHandler(uc))
		routing.RegisterRoute(r, handlers.NewSeedPredefinedHandler(uc))
		routing.RegisterRoute(r, handlers.NewCreateDataTemplateHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetDataTemplateByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateDataTemplateHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteDataTemplateHandler(uc))
	})
}
