package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/dto"
)

type ListEntityObligationsHandler struct {
	usecases domain.EntityObligationUseCases
}

func NewListEntityObligationsHandler(uc domain.EntityObligationUseCases) *ListEntityObligationsHandler {
	return &ListEntityObligationsHandler{usecases: uc}
}

func (h *ListEntityObligationsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:    http.MethodGet,
		Path:      "/",
		Exposure:  types.Exposures.External,
		Tenant:    true,
		Paginated: true,
	}
}

func (h *ListEntityObligationsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListEntityObligationsQuery{},
	}
}

func (h *ListEntityObligationsHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"createdAt":   "created_at",
		"periodicity": "periodicity",
	}
}

func (h *ListEntityObligationsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	result, err := h.usecases.List(r.Context(), *input.Pagination)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.EntityObligationToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}
