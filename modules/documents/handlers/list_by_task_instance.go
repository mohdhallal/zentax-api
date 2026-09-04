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

// ListByTaskInstanceHandler: GET /task-instances/{id}/documents.
type ListByTaskInstanceHandler struct {
	usecases domain.DocumentUseCases
}

func NewListByTaskInstanceHandler(uc domain.DocumentUseCases) *ListByTaskInstanceHandler {
	return &ListByTaskInstanceHandler{usecases: uc}
}

func (h *ListByTaskInstanceHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/task-instances/{id}/documents",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
	}
}

func (h *ListByTaskInstanceHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.TaskInstanceIdParams{},
	}
}

func (h *ListByTaskInstanceHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TaskInstanceIdParams)

	views, err := h.usecases.ListByTaskInstance(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(viewsToJSON(views)), nil
}
