package workflowtasks

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.WorkflowTaskUseCases) {
	router.Group("/workflow-tasks", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateWorkflowTaskHandler(uc))
		routing.RegisterRoute(r, handlers.NewListWorkflowTasksHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetWorkflowTaskByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateWorkflowTaskHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteWorkflowTaskHandler(uc))
	})
}
