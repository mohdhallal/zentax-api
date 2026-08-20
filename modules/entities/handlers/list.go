package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/modules/entities/dto"
)

type ListEntitiesHandler struct {
	usecases domain.EntityUseCases
}

func NewListEntitiesHandler(uc domain.EntityUseCases) *ListEntitiesHandler {
	return &ListEntitiesHandler{usecases: uc}
}

func (h *ListEntitiesHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:    http.MethodGet,
		Path:      "/",
		Exposure:  types.Exposures.External,
		Tenant:    true,
		Paginated: true,
	}
}

func (h *ListEntitiesHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListEntitiesQuery{},
	}
}

func (h *ListEntitiesHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"createdAt": "created_at",
		"name":      "name",
	}
}

func (h *ListEntitiesHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.EntityToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
