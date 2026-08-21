package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type DeleteObligationTypeHandler struct {
	usecases domain.ObligationTypeUseCases
}

func NewDeleteObligationTypeHandler(uc domain.ObligationTypeUseCases) *DeleteObligationTypeHandler {
	return &DeleteObligationTypeHandler{usecases: uc}
}

func (h *DeleteObligationTypeHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.ObligationTypeWrite,
	}
}

func (h *DeleteObligationTypeHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.ObligationTypeIdParams{},
	}
}

func (h *DeleteObligationTypeHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.ObligationTypeIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
