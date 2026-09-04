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

type CreateEntityHandler struct {
	usecases domain.EntityUseCases
}

func NewCreateEntityHandler(uc domain.EntityUseCases) *CreateEntityHandler {
	return &CreateEntityHandler{usecases: uc}
}

func (h *CreateEntityHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityWrite,
	}
}

func (h *CreateEntityHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateEntityBody{},
	}
}

func (h *CreateEntityHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateEntityBody)

	entity, err := h.usecases.Create(r.Context(), domain.CreateEntityInput{
		ParentEntityID:        body.ParentEntityID,
		Name:                  body.Name,
		LegalName:             body.LegalName,
		Country:               body.Country,
		TaxResidency:          body.TaxResidency,
		FiscalCalendarPattern: body.FiscalCalendarPattern,
		FinancialYearEnd:      body.FinancialYearEnd,
		FiscalWeekEndDay:      body.FiscalWeekEndDay,
		FiscalYearEndRule:     body.FiscalYearEndRule,
		CustomPeriods:         body.CustomPeriods,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Created(dto.EntityToJSON(entity)), nil
}
