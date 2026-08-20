package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/modules/entities/dto"
)

type GetEntityByIdHandler struct {
	usecases domain.EntityUseCases
}

func NewGetEntityByIdHandler(uc domain.EntityUseCases) *GetEntityByIdHandler {
	return &GetEntityByIdHandler{usecases: uc}
}

func (h *GetEntityByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetEntityByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.EntityIdParams{},
	}
}

func (h *GetEntityByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.EntityIdParams)

	entity, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.EntityToJSON(entity)), nil
}
