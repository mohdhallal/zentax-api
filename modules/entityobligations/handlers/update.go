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

type UpdateEntityObligationHandler struct {
	usecases domain.EntityObligationUseCases
}

func NewUpdateEntityObligationHandler(uc domain.EntityObligationUseCases) *UpdateEntityObligationHandler {
	return &UpdateEntityObligationHandler{usecases: uc}
}

func (h *UpdateEntityObligationHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityObligationWrite,
	}
}

func (h *UpdateEntityObligationHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateEntityObligationBody{},
		Params: dto.EntityObligationIdParams{},
	}
}

func (h *UpdateEntityObligationHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateEntityObligationBody)
	params, _ := input.Params.(*dto.EntityObligationIdParams)

	eo, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateEntityObligationInput{
		TaxReferenceNumber: body.TaxReferenceNumber,
		Jurisdiction:       body.Jurisdiction,
		JurisdictionState:  body.JurisdictionState,
		Currency:           body.Currency,
		Periodicity:        body.Periodicity,
		DeadlineRule:       body.DeadlineRule,
		Status:             body.Status,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.EntityObligationToJSON(eo)), nil
}
