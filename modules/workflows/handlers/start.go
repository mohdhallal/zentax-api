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

// StartWorkflowHandler handles POST /workflows/{id}/start — it materializes
// per-period task instances from the workflow's task templates.
type StartWorkflowHandler struct {
	starter domain.Starter
}

func NewStartWorkflowHandler(starter domain.Starter) *StartWorkflowHandler {
	return &StartWorkflowHandler{starter: starter}
}

func (h *StartWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/start",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowWrite,
	}
}

func (h *StartWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *StartWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	count, err := h.starter.StartWorkflow(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}

	return httpkit.Created(map[string]any{
		"workflowId":       params.ID,
		"instancesCreated": count,
	}), nil
}
