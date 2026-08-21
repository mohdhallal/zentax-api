package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// ApproveTaskInstanceHandler handles POST /task-instances/{id}/approve.
type ApproveTaskInstanceHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewApproveTaskInstanceHandler(uc domain.TaskInstanceUseCases) *ApproveTaskInstanceHandler {
	return &ApproveTaskInstanceHandler{usecases: uc}
}

func (h *ApproveTaskInstanceHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/approve",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskApprove,
	}
}

func (h *ApproveTaskInstanceHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.TaskInstanceIdParams{}}
}

func (h *ApproveTaskInstanceHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TaskInstanceIdParams)

	ti, err := h.usecases.Approve(r.Context(), params.ID, requester.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TaskInstanceToJSON(ti)), nil
}
