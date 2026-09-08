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

// TaskSummaryHandler serves GET /reports/task-summary: the exact counters the
// dashboard and the tasks page show (total / completed / active / overdue /
// due today / due this week / awaiting approval / completion rate / per-status
// counts) over the instances matching the shared task filters — one aggregate
// statement against the tenant's civil today, so the numbers stay right past
// any page cap. Not paginated: one object.
type TaskSummaryHandler struct {
	reader domain.Reader
}

func NewTaskSummaryHandler(reader domain.Reader) *TaskSummaryHandler {
	return &TaskSummaryHandler{reader: reader}
}

func (h *TaskSummaryHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/task-summary",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
	}
}

func (h *TaskSummaryHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.TaskSummaryQuery{}}
}

func (h *TaskSummaryHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.TaskSummaryQuery)
	summary, err := h.reader.TaskSummary(r.Context(), taskFilters(query.TaskFilterQuery))
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TaskSummaryToJSON(summary)), nil
}
