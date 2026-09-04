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

type GetDataTemplateByIdHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewGetDataTemplateByIdHandler(uc domain.DataTemplateUseCases) *GetDataTemplateByIdHandler {
	return &GetDataTemplateByIdHandler{usecases: uc}
}

func (h *GetDataTemplateByIdHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateRead,
	}
}

func (h *GetDataTemplateByIdHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DataTemplateIdParams{},
	}
}

func (h *GetDataTemplateByIdHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DataTemplateIdParams)

	t, err := h.usecases.GetById(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.DataTemplateToJSON(t)), nil
}
