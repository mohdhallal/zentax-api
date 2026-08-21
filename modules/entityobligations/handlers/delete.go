package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type DeleteEntityObligationHandler struct {
	usecases domain.EntityObligationUseCases
}

func NewDeleteEntityObligationHandler(uc domain.EntityObligationUseCases) *DeleteEntityObligationHandler {
	return &DeleteEntityObligationHandler{usecases: uc}
}

func (h *DeleteEntityObligationHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityObligationWrite,
	}
}

func (h *DeleteEntityObligationHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.EntityObligationIdParams{},
	}
}

func (h *DeleteEntityObligationHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.EntityObligationIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
