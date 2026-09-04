package handlers

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/modules/documents/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// DownloadDocumentHandler: GET /documents/{id}/download — streams the latest
// version. The handler writes the response itself and returns (nil, nil).
type DownloadDocumentHandler struct {
	usecases domain.DocumentUseCases
}

func NewDownloadDocumentHandler(uc domain.DocumentUseCases) *DownloadDocumentHandler {
	return &DownloadDocumentHandler{usecases: uc}
}

func (h *DownloadDocumentHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}/download",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
		Stream:     true, // the body goes straight to the wire (ADR-0022), never buffered
	}
}

func (h *DownloadDocumentHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentIdParams{},
	}
}

func (h *DownloadDocumentHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentIdParams)

	dl, err := h.usecases.Download(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return stream(w, r, dl)
}

// DownloadVersionHandler: GET /documents/{id}/versions/{versionId}/download.
type DownloadVersionHandler struct {
	usecases domain.DocumentUseCases
}

func NewDownloadVersionHandler(uc domain.DocumentUseCases) *DownloadVersionHandler {
	return &DownloadVersionHandler{usecases: uc}
}

func (h *DownloadVersionHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}/versions/{versionId}/download",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DocumentRead,
		Stream:     true, // the body goes straight to the wire (ADR-0022), never buffered
	}
}

func (h *DownloadVersionHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.DocumentVersionParams{},
	}
}

func (h *DownloadVersionHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.DocumentVersionParams)

	dl, err := h.usecases.DownloadVersion(r.Context(), params.ID, params.VersionID)
	if err != nil {
		return nil, err
	}
	return stream(w, r, dl)
}

// stream writes the blob with the download headers (ADR-0022 decision 2):
// the stored MIME type, exact length, an attachment disposition carrying both
// an ASCII-sanitized and an RFC 5987 UTF-8 file name, no caching, no sniffing.
// Every error path that can still answer JSON has been taken before this point.
func stream(w http.ResponseWriter, r *http.Request, dl *domain.Download) (*types.HttpResponse, error) {
	defer dl.Body.Close()

	hdr := w.Header()
	if requestID := app.GetRequestId(r.Context()); requestID != "" {
		hdr.Set("X-Request-Id", requestID)
	}
	hdr.Set("Content-Type", dl.ContentType)
	hdr.Set("Content-Length", strconv.FormatInt(dl.Version.FileSize, 10))
	hdr.Set("Content-Disposition", contentDisposition(dl.Version.FileName))
	hdr.Set("Cache-Control", "private, no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, dl.Body); err != nil {
		// Headers are out; the client sees a truncated body. Log, nothing else.
		logger.Log.WithContext(r.Context()).Warn("documents: download stream interrupted",
			logger.String("documentId", dl.Version.DocumentID), logger.Error(err))
	}
	return nil, nil //nolint:nilnil // (nil, nil) = the handler wrote the response
}

// contentDisposition renders `attachment; filename="<ascii>"; filename*=UTF-8”<pct>`.
func contentDisposition(name string) string {
	return `attachment; filename="` + asciiFileName(name) + `"; filename*=UTF-8''` + rfc5987(name)
}

// asciiFileName keeps printable ASCII, replacing everything else (and the
// quote / backslash that would break the quoted-string) with "_".
func asciiFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('_')
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "download"
	}
	return b.String()
}

// rfc5987 percent-encodes everything outside attr-char (RFC 5987 §3.2.1).
func rfc5987(name string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(name) {
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}
