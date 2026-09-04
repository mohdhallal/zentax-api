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

// EntityPeriodsHandler handles GET /entities/{id}/periods — the single source
// of period options for the UI (ADR-0023): the rows come from the same
// calendar engine the workflow generator uses. 404 for an unknown / foreign
// entity, 400 with the engine's message for a combination it cannot compute.
type EntityPeriodsHandler struct {
	usecases domain.EntityUseCases
}

func NewEntityPeriodsHandler(uc domain.EntityUseCases) *EntityPeriodsHandler {
	return &EntityPeriodsHandler{usecases: uc}
}

func (h *EntityPeriodsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}/periods",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.EntityRead,
	}
}

func (h *EntityPeriodsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.EntityIdParams{},
		Query:  dto.PeriodsQuery{},
	}
}

func (h *EntityPeriodsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.EntityIdParams)
	query, _ := input.Query.(*dto.PeriodsQuery)

	periods, err := h.usecases.Periods(r.Context(), params.ID, query.Periodicity, query.FinancialYear)
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.PeriodsToJSON(periods)), nil
}
