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

// ListByWorkflowHandler: GET /workflows/{id}/documents — latest versions of
// the workflow's live documents, newest first.
type ListByWorkflowHandler struct {
	usecases domain.DocumentUseCases
}

func NewListByWorkflowHandler(uc domain.DocumentUseCases) *ListByWorkflowHandler {
	return &ListByWorkflowHandler{usecases: uc}
}

func (h *ListByWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/workflows/{id}/documents",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
	}
}

func (h *ListByWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *ListByWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	views, err := h.usecases.ListByWorkflow(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(viewsToJSON(views)), nil
}

func viewsToJSON(views []domain.DocumentView) []map[string]any {
	data := make([]map[string]any, 0, len(views))
	for i := range views {
		data = append(data, dto.DocumentViewToJSON(&views[i]))
	}
	return data
}
