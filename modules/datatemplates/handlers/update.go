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

type UpdateDataTemplateHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewUpdateDataTemplateHandler(uc domain.DataTemplateUseCases) *UpdateDataTemplateHandler {
	return &UpdateDataTemplateHandler{usecases: uc}
}

func (h *UpdateDataTemplateHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateWrite,
	}
}

func (h *UpdateDataTemplateHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateDataTemplateBody{},
		Params: dto.DataTemplateIdParams{},
	}
}

func (h *UpdateDataTemplateHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateDataTemplateBody)
	params, _ := input.Params.(*dto.DataTemplateIdParams)

	t, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateDataTemplateInput{
		Name:         body.Name,
		TemplateType: body.TemplateType,
		Description:  body.Description,
		Fields:       dto.FieldsToDomain(body.Fields),
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.DataTemplateToJSON(t)), nil
}
