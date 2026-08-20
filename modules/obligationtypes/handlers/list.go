package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/dto"
)

type ListObligationTypesHandler struct {
	usecases domain.ObligationTypeUseCases
}

func NewListObligationTypesHandler(uc domain.ObligationTypeUseCases) *ListObligationTypesHandler {
	return &ListObligationTypesHandler{usecases: uc}
}

func (h *ListObligationTypesHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:    http.MethodGet,
		Path:      "/",
		Exposure:  types.Exposures.External,
		Tenant:    true,
		Paginated: true,
	}
}

func (h *ListObligationTypesHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListObligationTypesQuery{},
	}
}

func (h *ListObligationTypesHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"createdAt": "created_at",
		"name":      "name",
		"code":      "code",
	}
}

func (h *ListObligationTypesHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.ObligationTypeToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
