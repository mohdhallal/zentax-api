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

// WorkflowStatsHandler serves GET /reports/workflow-stats: an object keyed by
// workflow id with completion counts and the next due date, for every workflow
// matching the optional filters (workflowId, entityId, financialYear incl.
// `none`, status, workflowCategory) — all of the tenant's when none is given,
// instance-less workflows included at zero. Not paginated — one aggregate row
// per workflow.
type WorkflowStatsHandler struct {
	reader domain.Reader
}

func NewWorkflowStatsHandler(reader domain.Reader) *WorkflowStatsHandler {
	return &WorkflowStatsHandler{reader: reader}
}

func (h *WorkflowStatsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/workflow-stats",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowRead,
	}
}

func (h *WorkflowStatsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.WorkflowStatsQuery{}}
}

func (h *WorkflowStatsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.WorkflowStatsQuery)
	stats, err := h.reader.WorkflowStats(r.Context(), workflowStatsFilters(*query))
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.WorkflowStatsToJSON(stats)), nil
}
