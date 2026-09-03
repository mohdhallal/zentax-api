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

// PreviewWorkflowHandler handles GET /workflows/{id}/preview — a dry run of
// start: the summary and every task instance start would create, computed by
// the same planner, without creating anything. A read capability suffices: a
// scoped viewer may see what a run would produce, only a writer may run it.
type PreviewWorkflowHandler struct {
	starter domain.Starter
}

func NewPreviewWorkflowHandler(starter domain.Starter) *PreviewWorkflowHandler {
	return &PreviewWorkflowHandler{starter: starter}
}

func (h *PreviewWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}/preview",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowRead,
	}
}

func (h *PreviewWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *PreviewWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)
	preview, err := h.starter.PreviewWorkflow(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(preview), nil
}
