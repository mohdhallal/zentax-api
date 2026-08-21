package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
)

type GetWorkflowByIdHandler struct {
	usecases domain.WorkflowUseCases
}

func NewGetWorkflowByIdHandler(uc domain.WorkflowUseCases) *GetWorkflowByIdHandler {
	return &GetWorkflowByIdHandler{usecases: uc}
}

func (h *GetWorkflowByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetWorkflowByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *GetWorkflowByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	wf, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.WorkflowToJSON(wf)), nil
}
