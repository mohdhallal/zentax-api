package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/modules/entities/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type DeleteEntityHandler struct {
	usecases domain.EntityUseCases
}

func NewDeleteEntityHandler(uc domain.EntityUseCases) *DeleteEntityHandler {
	return &DeleteEntityHandler{usecases: uc}
}

func (h *DeleteEntityHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityWrite,
	}
}

func (h *DeleteEntityHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.EntityIdParams{},
	}
}

func (h *DeleteEntityHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.EntityIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
