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

type UpdateEntityHandler struct {
	usecases domain.EntityUseCases
}

func NewUpdateEntityHandler(uc domain.EntityUseCases) *UpdateEntityHandler {
	return &UpdateEntityHandler{usecases: uc}
}

func (h *UpdateEntityHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityWrite,
	}
}

func (h *UpdateEntityHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateEntityBody{},
		Params: dto.EntityIdParams{},
	}
}

func (h *UpdateEntityHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateEntityBody)
	params, _ := input.Params.(*dto.EntityIdParams)

	entity, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateEntityInput{
		ParentEntityID:        body.ParentEntityID,
		Name:                  body.Name,
		LegalName:             body.LegalName,
		Country:               body.Country,
		TaxResidency:          body.TaxResidency,
		FiscalCalendarPattern: body.FiscalCalendarPattern,
		FinancialYearEnd:      body.FinancialYearEnd,
		Status:                body.Status,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.EntityToJSON(entity)), nil
}
