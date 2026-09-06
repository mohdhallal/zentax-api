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

// ComplianceStatusHandler serves GET /reports/compliance-status: one row per
// task instance classified on_time / late / missed / not_due against its
// filing deadline, as a capped page (default 1000, max 5000) of the
// status-filtered set with its exact totalCount (ADR-0021 rule 2), plus a
// summary over the whole classified set (year / entity / obligation filters
// applied, the status filter not).
type ComplianceStatusHandler struct {
	reader domain.Reader
}

func NewComplianceStatusHandler(reader domain.Reader) *ComplianceStatusHandler {
	return &ComplianceStatusHandler{reader: reader}
}

func (h *ComplianceStatusHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/compliance-status",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
	}
}

func (h *ComplianceStatusHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.ComplianceStatusQuery{}}
}

func (h *ComplianceStatusHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ComplianceStatusQuery)
	filters, err := reportFilters(query.ReportFilterQuery)
	if err != nil {
		return nil, err
	}
	status, err := optionalEnum("status", query.Status, "on_time", "late", "missed", "not_due")
	if err != nil {
		return nil, err
	}
	res, err := h.reader.ComplianceStatus(r.Context(), domain.ComplianceStatusArgs{
		ReportFilters: filters,
		Status:        status,
		Limit:         query.Limit,
		Offset:        query.Offset,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.ComplianceStatusToJSON(res)), nil
}
