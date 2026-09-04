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

// UpdateDocumentHandler: PUT /documents/{id} — partial metadata update
// (omitted = unchanged).
type UpdateDocumentHandler struct {
	usecases domain.DocumentUseCases
}

func NewUpdateDocumentHandler(uc domain.DocumentUseCases) *UpdateDocumentHandler {
	return &UpdateDocumentHandler{usecases: uc}
}

func (h *UpdateDocumentHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentWrite,
	}
}

func (h *UpdateDocumentHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateDocumentBody{},
		Params: dto.DocumentIdParams{},
	}
}

func (h *UpdateDocumentHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateDocumentBody)
	params, _ := input.Params.(*dto.DocumentIdParams)

	view, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateDocumentInput{
		Label:        body.Label,
		Notes:        body.Notes,
		DocumentType: body.DocumentType,
		Category:     body.Category,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.DocumentViewToJSON(view)), nil
}
