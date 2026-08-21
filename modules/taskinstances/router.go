package taskinstances

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.TaskInstanceUseCases) {
	router.Group("/task-instances", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewListTaskInstancesHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetTaskInstanceByIdHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateTaskInstanceHandler(uc))
		routing.RegisterRoute(r, handlers.NewSubmitTaskInstanceHandler(uc))
		routing.RegisterRoute(r, handlers.NewApproveTaskInstanceHandler(uc))
		routing.RegisterRoute(r, handlers.NewRejectTaskInstanceHandler(uc))
	})
}
