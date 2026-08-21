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

// SubmitTaskInstanceHandler handles POST /task-instances/{id}/submit-for-approval.
type SubmitTaskInstanceHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewSubmitTaskInstanceHandler(uc domain.TaskInstanceUseCases) *SubmitTaskInstanceHandler {
	return &SubmitTaskInstanceHandler{usecases: uc}
}

func (h *SubmitTaskInstanceHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/submit-for-approval",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskSubmit,
	}
}

func (h *SubmitTaskInstanceHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.TaskInstanceIdParams{}}
}

func (h *SubmitTaskInstanceHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TaskInstanceIdParams)

	ti, err := h.usecases.SubmitForApproval(r.Context(), params.ID, requester.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TaskInstanceToJSON(ti)), nil
}
