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
// per-period task instances from the workflow's task templates, applying the
// caller's optional per-instance date overrides (keys as listed by
// GET /workflows/{id}/preview).
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
		Body:   dto.StartWorkflowBody{}, // optional: an absent body validates to the zero value
	}
}

func (h *StartWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	var overrides domain.TaskOverrides
	if body, ok := input.Body.(*dto.StartWorkflowBody); ok && body != nil {
		var err error
		if overrides, err = body.ToDomain(); err != nil {
			return nil, err
		}
	}

	count, err := h.starter.StartWorkflow(r.Context(), params.ID, overrides)
	if err != nil {
		return nil, err
	}

	return httpkit.Created(map[string]any{
		"workflowId":       params.ID,
		"instancesCreated": count,
	}), nil
}
