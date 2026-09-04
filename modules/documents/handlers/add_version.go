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

// AddVersionHandler: POST /documents/{id}/versions (multipart: file, label?).
type AddVersionHandler struct {
	usecases domain.DocumentUseCases
	maxBytes int64
}

func NewAddVersionHandler(uc domain.DocumentUseCases, maxBytes int64) *AddVersionHandler {
	return &AddVersionHandler{usecases: uc, maxBytes: maxBytes}
}

func (h *AddVersionHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/versions",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentWrite,
	}
}

func (h *AddVersionHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentIdParams{},
	}
}

func (h *AddVersionHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentIdParams)

	up, err := parseUpload(w, r, h.maxBytes, "label")
	if err != nil {
		return nil, err
	}
	defer closeBody(up)

	view, err := h.usecases.AddVersion(r.Context(), params.ID, domain.AddVersionInput{
		Label: up.optional("label"),
		File:  up.File,
	})
	if err != nil {
		return nil, mapUploadError(err)
	}
	return httpkit.Created(dto.DocumentViewToJSON(view)), nil
}
