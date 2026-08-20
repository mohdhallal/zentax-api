package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/dto"
)

type UpdateObligationTypeHandler struct {
	usecases domain.ObligationTypeUseCases
}

func NewUpdateObligationTypeHandler(uc domain.ObligationTypeUseCases) *UpdateObligationTypeHandler {
	return &UpdateObligationTypeHandler{usecases: uc}
}

func (h *UpdateObligationTypeHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPut,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *UpdateObligationTypeHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateObligationTypeBody{},
		Params: dto.ObligationTypeIdParams{},
	}
}

func (h *UpdateObligationTypeHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateObligationTypeBody)
	params, _ := input.Params.(*dto.ObligationTypeIdParams)

	ot, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateObligationTypeInput{
		Name:        body.Name,
		Code:        body.Code,
		Category:    body.Category,
		Template:    body.Template,
		Status:      body.Status,
		Description: body.Description,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.ObligationTypeToJSON(ot)), nil
}
