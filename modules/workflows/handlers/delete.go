package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
)

type DeleteWorkflowHandler struct {
	usecases domain.WorkflowUseCases
}

func NewDeleteWorkflowHandler(uc domain.WorkflowUseCases) *DeleteWorkflowHandler {
	return &DeleteWorkflowHandler{usecases: uc}
}

func (h *DeleteWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodDelete,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *DeleteWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *DeleteWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
