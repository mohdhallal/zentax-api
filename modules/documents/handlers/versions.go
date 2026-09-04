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

// ListVersionsHandler: GET /documents/{id}/versions — every version, newest first.
type ListVersionsHandler struct {
	usecases domain.DocumentUseCases
}

func NewListVersionsHandler(uc domain.DocumentUseCases) *ListVersionsHandler {
	return &ListVersionsHandler{usecases: uc}
}

func (h *ListVersionsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}/versions",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
	}
}

func (h *ListVersionsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentIdParams{},
	}
}

func (h *ListVersionsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentIdParams)

	versions, err := h.usecases.ListVersions(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	data := make([]map[string]any, 0, len(versions))
	for i := range versions {
		data = append(data, dto.DocumentVersionToJSON(&versions[i]))
	}
	return httpkit.Ok(data), nil
}
