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
// in the tenant (instance-less workflows included, at zero). Not paginated —
// one aggregate row per workflow.
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

func (h *WorkflowStatsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	stats, err := h.reader.WorkflowStats(r.Context())
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.WorkflowStatsToJSON(stats)), nil
}
