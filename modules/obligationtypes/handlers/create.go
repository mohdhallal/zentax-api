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

type CreateObligationTypeHandler struct {
	usecases domain.ObligationTypeUseCases
}

func NewCreateObligationTypeHandler(uc domain.ObligationTypeUseCases) *CreateObligationTypeHandler {
	return &CreateObligationTypeHandler{usecases: uc}
}

func (h *CreateObligationTypeHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.ObligationTypeWrite,
	}
}

func (h *CreateObligationTypeHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateObligationTypeBody{},
	}
}

func (h *CreateObligationTypeHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateObligationTypeBody)

	ot, err := h.usecases.Create(r.Context(), domain.CreateObligationTypeInput{
		Name:        body.Name,
		Code:        body.Code,
		Category:    body.Category,
		Template:    body.Template,
		Description: body.Description,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Created(dto.ObligationTypeToJSON(ot)), nil
}
