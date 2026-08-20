package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/dto"
)

type GetEntityObligationByIdHandler struct {
	usecases domain.EntityObligationUseCases
}

func NewGetEntityObligationByIdHandler(uc domain.EntityObligationUseCases) *GetEntityObligationByIdHandler {
	return &GetEntityObligationByIdHandler{usecases: uc}
}

func (h *GetEntityObligationByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetEntityObligationByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.EntityObligationIdParams{},
	}
}

func (h *GetEntityObligationByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.EntityObligationIdParams)

	eo, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.EntityObligationToJSON(eo)), nil
}
