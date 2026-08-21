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

// RejectTaskInstanceHandler handles POST /task-instances/{id}/reject.
type RejectTaskInstanceHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewRejectTaskInstanceHandler(uc domain.TaskInstanceUseCases) *RejectTaskInstanceHandler {
	return &RejectTaskInstanceHandler{usecases: uc}
}

func (h *RejectTaskInstanceHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/reject",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskApprove,
	}
}

func (h *RejectTaskInstanceHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.RejectTaskInstanceBody{},
		Params: dto.TaskInstanceIdParams{},
	}
}

func (h *RejectTaskInstanceHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TaskInstanceIdParams)
	var reason *string
	if body, ok := input.Body.(*dto.RejectTaskInstanceBody); ok {
		reason = body.Reason
	}

	ti, err := h.usecases.Reject(r.Context(), params.ID, reason)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TaskInstanceToJSON(ti)), nil
}
