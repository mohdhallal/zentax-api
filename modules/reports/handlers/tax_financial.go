package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// TaxFinancialHandler serves GET /reports/tax-financial: financial figures
// extracted from tax_data (fixed key set, safe numeric cast) — a capped page
// of rows plus exact aggregates (by groupBy, per period, grand total) from
// one GROUPING SETS statement over the same filtered set (ADR-0021).
type TaxFinancialHandler struct {
	reader domain.Reader
}

func NewTaxFinancialHandler(reader domain.Reader) *TaxFinancialHandler {
	return &TaxFinancialHandler{reader: reader}
}

func (h *TaxFinancialHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/tax-financial",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
	}
}

func (h *TaxFinancialHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.TaxFinancialQuery{}}
}

func (h *TaxFinancialHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.TaxFinancialQuery)
	filters, err := reportFilters(query.ReportFilterQuery)
	if err != nil {
		return nil, err
	}
	res, err := h.reader.TaxFinancial(r.Context(), domain.TaxFinancialArgs{
		ReportFilters: filters,
		GroupBy:       query.GroupBy,
		Limit:         query.Limit,
		Offset:        query.Offset,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TaxFinancialToJSON(res)), nil
}
