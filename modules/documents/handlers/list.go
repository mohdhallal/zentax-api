package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/modules/documents/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// ListDocumentsHandler: GET /documents — the repository view (paginated,
// filters entityId / workflowId / taskInstanceId / documentType / year
// (repeatable, `none` = no financial year) / search).
type ListDocumentsHandler struct {
	usecases domain.DocumentUseCases
}

func NewListDocumentsHandler(uc domain.DocumentUseCases) *ListDocumentsHandler {
	return &ListDocumentsHandler{usecases: uc}
}

func (h *ListDocumentsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
		Paginated:  true,
	}
}

func (h *ListDocumentsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListDocumentsQuery{},
	}
}

func (h *ListDocumentsHandler) DefineSortColumns() map[string]string {
	return map[string]string{"createdAt": "created_at"}
}

func (h *ListDocumentsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	q, _ := input.Query.(*dto.ListDocumentsQuery)

	filter := domain.ListDocumentsFilter{
		EntityID:       dto.Filter(q.EntityID),
		WorkflowID:     dto.Filter(q.WorkflowID),
		TaskInstanceID: dto.Filter(q.TaskInstanceID),
		DocumentType:   dto.Filter(q.DocumentType),
		Years:          dto.Filters(q.Year),
		Search:         dto.SearchTerm(q.Search),
		Limit:          q.Limit,
		Offset:         q.Offset,
	}
	views, total, err := h.usecases.List(r.Context(), filter)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(viewsToJSON(views)).WithPagination(types.Pagination{
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	}), nil
}
