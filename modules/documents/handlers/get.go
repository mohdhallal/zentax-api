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

// GetDocumentHandler: GET /documents/{id} (soft-deleted → 404).
type GetDocumentHandler struct {
	usecases domain.DocumentUseCases
}

func NewGetDocumentHandler(uc domain.DocumentUseCases) *GetDocumentHandler {
	return &GetDocumentHandler{usecases: uc}
}

func (h *GetDocumentHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
	}
}

func (h *GetDocumentHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentIdParams{},
	}
}

func (h *GetDocumentHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentIdParams)

	view, err := h.usecases.Get(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.DocumentViewToJSON(view)), nil
}
