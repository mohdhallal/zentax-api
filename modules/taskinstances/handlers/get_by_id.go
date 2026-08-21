package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/dto"
)

type GetTaskInstanceByIdHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewGetTaskInstanceByIdHandler(uc domain.TaskInstanceUseCases) *GetTaskInstanceByIdHandler {
	return &GetTaskInstanceByIdHandler{usecases: uc}
}

func (h *GetTaskInstanceByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetTaskInstanceByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.TaskInstanceIdParams{},
	}
}

func (h *GetTaskInstanceByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TaskInstanceIdParams)

	ti, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.TaskInstanceToJSON(ti)), nil
}
