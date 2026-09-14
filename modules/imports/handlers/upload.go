// Package handlers exposes spreadsheet ingest over HTTP: upload (which is the
// dry run), the row-by-row report, and the commit.
//
// Every handler is built for ONE kind and carries it, rather than reading it
// from the path. That is what lets each route declare the capability it needs
// (entity:write for an entity file, entity_obligation:write for an obligations
// file) in DefineRoute, where the framework enforces it before the handler
// runs — a kind read out of the URL at request time could not be declared, and
// the tenant-wide gate would have to move into the use case, out of sight of
// anyone reading the route table.
package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/dto"
)

// Bounds is the configured ceiling on one upload, passed in rather than read
// from a package variable so the composition root remains the only place that
// decides it.
type Bounds struct {
	MaxFileBytes int64
	MaxRows      int
}

// UploadHandler: POST /imports/{kind} (multipart/form-data: file).
//
// The response is the whole dry run — the batch, every planned row, and how the
// header was read — because the customer's next action depends on all three and
// a second round trip to fetch the rows would let the two answers be different.
// A large file's report is paged through GET /imports/{kind}/{id}/rows instead.
type UploadHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
	bounds   Bounds
}

func NewUploadHandler(uc domain.ImportUseCases, kind domain.Kind, bounds Bounds) *UploadHandler {
	return &UploadHandler{usecases: uc, kind: kind, bounds: bounds}
}

func (h *UploadHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/" + string(h.kind.Target()),
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: writeCapability(h.kind),
	}
}

// DefineSchema declares no Body: the multipart stream is read by the handler,
// so the router leaves r.Body alone.
func (h *UploadHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{}
}

func (h *UploadHandler) Execute(
	w http.ResponseWriter, r *http.Request, _ *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	fileName, content, checksum, err := readUpload(w, r, h.bounds.MaxFileBytes)
	if err != nil {
		return nil, err
	}

	batch, plan, err := h.usecases.Validate(r.Context(), domain.Upload{
		Target:   h.kind.Target(),
		FileName: fileName,
		Content:  content,
		Checksum: checksum,
		MaxRows:  h.bounds.MaxRows,
	})
	if err != nil {
		return nil, mapReadError(err, h.bounds.MaxRows)
	}

	// 201: a batch was created. It is not an import — nothing of the customer's
	// data has changed — and the body says so in `status` and `committable`.
	return httpkit.Created(dto.DryRunJSON{
		Batch:  dto.BatchToJSON(batch),
		Rows:   dto.RowsToJSON(plan.Rows),
		Issues: nonNilIssues(plan.FileIssues),
		Columns: dto.ColumnsJSON{
			HeaderRow: plan.HeaderRow,
			Bound:     nonNilColumns(plan.Columns),
			Ignored:   nonNilIgnored(plan.Ignored),
		},
	}), nil
}

func nonNilIssues(issues []domain.Issue) []domain.Issue {
	if issues == nil {
		return []domain.Issue{}
	}
	return issues
}

func nonNilColumns(columns map[string]string) map[string]string {
	if columns == nil {
		return map[string]string{}
	}
	return columns
}

func nonNilIgnored(ignored []domain.IgnoredColumn) []domain.IgnoredColumn {
	if ignored == nil {
		return []domain.IgnoredColumn{}
	}
	return ignored
}
