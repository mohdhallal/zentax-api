package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type DeleteWorkflowTaskHandler struct {
	usecases domain.WorkflowTaskUseCases
}

func NewDeleteWorkflowTaskHandler(uc domain.WorkflowTaskUseCases) *DeleteWorkflowTaskHandler {
	return &DeleteWorkflowTaskHandler{usecases: uc}
}

func (h *DeleteWorkflowTaskHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowTaskWrite,
	}
}

func (h *DeleteWorkflowTaskHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowTaskIdParams{},
	}
}

func (h *DeleteWorkflowTaskHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowTaskIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
