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

// UploadDocumentHandler: POST /workflows/{id}/documents (multipart/form-data:
// file, documentType, label?, notes?, taskInstanceId?, category?).
type UploadDocumentHandler struct {
	usecases domain.DocumentUseCases
	maxBytes int64
}

func NewUploadDocumentHandler(uc domain.DocumentUseCases, maxBytes int64) *UploadDocumentHandler {
	return &UploadDocumentHandler{usecases: uc, maxBytes: maxBytes}
}

func (h *UploadDocumentHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/workflows/{id}/documents",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentWrite,
	}
}

// DefineSchema declares no Body: the multipart stream is read by the handler.
func (h *UploadDocumentHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.WorkflowIdParams{},
	}
}

func (h *UploadDocumentHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.WorkflowIdParams)

	up, err := parseUpload(w, r, h.maxBytes, "documentType", "label", "notes", "taskInstanceId", "category")
	if err != nil {
		return nil, err
	}
	defer closeBody(up)

	in := domain.CreateDocumentInput{
		WorkflowID:     params.ID,
		TaskInstanceID: nonEmpty(up.optional("taskInstanceId")),
		Category:       up.Fields["category"],
		DocumentType:   up.Fields["documentType"],
		Label:          nonEmpty(up.optional("label")),
		Notes:          nonEmpty(up.optional("notes")),
		File:           up.File,
	}
	if in.TaskInstanceID != nil && !isUUID(*in.TaskInstanceID) {
		return nil, validation("taskInstanceId must be a uuid")
	}

	view, err := h.usecases.Create(r.Context(), in)
	if err != nil {
		return nil, mapUploadError(err)
	}
	return httpkit.Created(dto.DocumentViewToJSON(view)), nil
}
