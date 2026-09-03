package workflows

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.WorkflowUseCases, starter domain.Starter) {
	router.Group("/workflows", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateWorkflowHandler(uc))
		routing.RegisterRoute(r, handlers.NewListWorkflowsHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetWorkflowByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateWorkflowHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteWorkflowHandler(uc))
		routing.RegisterRoute(r, handlers.NewPreviewWorkflowHandler(starter))
		routing.RegisterRoute(r, handlers.NewStartWorkflowHandler(starter))
	})
}
