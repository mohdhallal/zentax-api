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

// ComplianceHeatmapHandler serves GET /reports/compliance-heatmap: entities ×
// (periods | obligation types) traffic-light cells from one GROUP BY
// (ADR-0021). Not paginated — the grid is bounded by entities × columns.
type ComplianceHeatmapHandler struct {
	reader domain.Reader
}

func NewComplianceHeatmapHandler(reader domain.Reader) *ComplianceHeatmapHandler {
	return &ComplianceHeatmapHandler{reader: reader}
}

func (h *ComplianceHeatmapHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/compliance-heatmap",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
	}
}

func (h *ComplianceHeatmapHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.ComplianceHeatmapQuery{}}
}

func (h *ComplianceHeatmapHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ComplianceHeatmapQuery)
	filters, err := reportFilters(query.ReportFilterQuery)
	if err != nil {
		return nil, err
	}
	cells, err := h.reader.ComplianceHeatmap(r.Context(), domain.HeatmapArgs{
		ReportFilters: filters,
		ViewMode:      query.ViewMode,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.HeatmapToJSON(cells)), nil
}
