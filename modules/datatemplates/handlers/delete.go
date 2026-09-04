package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type DeleteDataTemplateHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewDeleteDataTemplateHandler(uc domain.DataTemplateUseCases) *DeleteDataTemplateHandler {
	return &DeleteDataTemplateHandler{usecases: uc}
}

func (h *DeleteDataTemplateHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateWrite,
	}
}

func (h *DeleteDataTemplateHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DataTemplateIdParams{},
	}
}

func (h *DeleteDataTemplateHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DataTemplateIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}

	return httpkit.NoContent(), nil
}
