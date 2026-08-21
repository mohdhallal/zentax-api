package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/dto"
)

type GetWorkflowTaskByIdHandler struct {
	usecases domain.WorkflowTaskUseCases
}

func NewGetWorkflowTaskByIdHandler(uc domain.WorkflowTaskUseCases) *GetWorkflowTaskByIdHandler {
	return &GetWorkflowTaskByIdHandler{usecases: uc}
}

func (h *GetWorkflowTaskByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetWorkflowTaskByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowTaskIdParams{},
	}
}

func (h *GetWorkflowTaskByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowTaskIdParams)

	wt, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.WorkflowTaskToJSON(wt)), nil
}
