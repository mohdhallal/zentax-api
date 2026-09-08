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

// TaskInstancesReportHandler serves GET /reports/task-instances: task
// instances enriched with workflow / entity / obligation-type context and the
// assignee's display name — what the frontend's task views render without a
// waterfall of per-row lookups. Filters are the set shared with
// /reports/task-summary (`status=open` = not completed), so the dashboard's
// priority list is `status=open&sort=dueDate:asc&limit=5` over the same
// population its tiles count.
type TaskInstancesReportHandler struct {
	reader domain.Reader
}

func NewTaskInstancesReportHandler(reader domain.Reader) *TaskInstancesReportHandler {
	return &TaskInstancesReportHandler{reader: reader}
}

func (h *TaskInstancesReportHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/task-instances",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
		Paginated:  true,
	}
}

func (h *TaskInstancesReportHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListTaskInstancesQuery{},
	}
}

func (h *TaskInstancesReportHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"dueDate":   domain.SortByDueDate,
		"createdAt": domain.SortByCreatedAt,
	}
}

func (h *TaskInstancesReportHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ListTaskInstancesQuery)

	args := domain.ListTaskInstancesArgs{
		TaskFilters: taskFilters(query.TaskFilterQuery),
		SortColumn:  domain.SortByDueDate, // contract default: dueDate:asc
		Limit:       query.Limit,
		Offset:      query.Offset,
	}
	// The pagination plumbing has already mapped the validated sort key to a
	// column through DefineSortColumns; a single key is honored.
	if input.Pagination != nil && len(input.Pagination.Sort) > 0 {
		args.SortColumn = input.Pagination.Sort[0].Column
		args.SortDesc = input.Pagination.Sort[0].Desc
	}

	rows, total, err := h.reader.ListTaskInstances(r.Context(), args)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, dto.TaskInstanceRowToJSON(&rows[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  total,
		Limit:  args.Limit,
		Offset: args.Offset,
	}), nil
}
