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

// DeleteDocumentHandler: DELETE /documents/{id} — soft delete (204).
type DeleteDocumentHandler struct {
	usecases domain.DocumentUseCases
}

func NewDeleteDocumentHandler(uc domain.DocumentUseCases) *DeleteDocumentHandler {
	return &DeleteDocumentHandler{usecases: uc}
}

func (h *DeleteDocumentHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentWrite,
	}
}

func (h *DeleteDocumentHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentIdParams{},
	}
}

func (h *DeleteDocumentHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentIdParams)

	if err := h.usecases.Delete(r.Context(), params.ID); err != nil {
		return nil, err
	}
	return httpkit.NoContent(), nil
}
