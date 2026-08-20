package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/dto"
)

type GetObligationTypeByIdHandler struct {
	usecases domain.ObligationTypeUseCases
}

func NewGetObligationTypeByIdHandler(uc domain.ObligationTypeUseCases) *GetObligationTypeByIdHandler {
	return &GetObligationTypeByIdHandler{usecases: uc}
}

func (h *GetObligationTypeByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *GetObligationTypeByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.ObligationTypeIdParams{},
	}
}

func (h *GetObligationTypeByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.ObligationTypeIdParams)

	ot, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.ObligationTypeToJSON(ot)), nil
}
