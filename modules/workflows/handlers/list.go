package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type ListWorkflowsHandler struct {
	usecases domain.WorkflowUseCases
}

func NewListWorkflowsHandler(uc domain.WorkflowUseCases) *ListWorkflowsHandler {
	return &ListWorkflowsHandler{usecases: uc}
}

func (h *ListWorkflowsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowRead,
		Paginated:  true,
	}
}

func (h *ListWorkflowsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListWorkflowsQuery{},
	}
}

func (h *ListWorkflowsHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"createdAt": "created_at",
		"name":      "name",
	}
}

func (h *ListWorkflowsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.WorkflowToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
