package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type ListWorkflowTasksHandler struct {
	usecases domain.WorkflowTaskUseCases
}

func NewListWorkflowTasksHandler(uc domain.WorkflowTaskUseCases) *ListWorkflowTasksHandler {
	return &ListWorkflowTasksHandler{usecases: uc}
}

func (h *ListWorkflowTasksHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowTaskRead,
		Paginated:  true,
	}
}

func (h *ListWorkflowTasksHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListWorkflowTasksQuery{},
	}
}

func (h *ListWorkflowTasksHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"orderIndex": "order_index",
		"createdAt":  "created_at",
	}
}

func (h *ListWorkflowTasksHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.WorkflowTaskToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
