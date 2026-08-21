package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type ListTaskInstancesHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewListTaskInstancesHandler(uc domain.TaskInstanceUseCases) *ListTaskInstancesHandler {
	return &ListTaskInstancesHandler{usecases: uc}
}

func (h *ListTaskInstancesHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
		Paginated:  true,
	}
}

func (h *ListTaskInstancesHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListTaskInstancesQuery{},
	}
}

func (h *ListTaskInstancesHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"dueDate":   "due_date",
		"createdAt": "created_at",
	}
}

func (h *ListTaskInstancesHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.TaskInstanceToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
