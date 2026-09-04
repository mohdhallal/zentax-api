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

type CreateDataTemplateHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewCreateDataTemplateHandler(uc domain.DataTemplateUseCases) *CreateDataTemplateHandler {
	return &CreateDataTemplateHandler{usecases: uc}
}

func (h *CreateDataTemplateHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateWrite,
	}
}

func (h *CreateDataTemplateHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateDataTemplateBody{},
	}
}

func (h *CreateDataTemplateHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateDataTemplateBody)

	t, err := h.usecases.Create(r.Context(), domain.CreateDataTemplateInput{
		Name:         body.Name,
		TemplateType: body.TemplateType,
		Description:  body.Description,
		Fields:       dto.FieldsToDomain(body.Fields),
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Created(dto.DataTemplateToJSON(t)), nil
}
