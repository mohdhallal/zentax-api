package handlers

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/dto"
)

// GetBatchHandler: GET /imports/{kind}/{id} — one batch and its summary.
type GetBatchHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
}

func NewGetBatchHandler(uc domain.ImportUseCases, kind domain.Kind) *GetBatchHandler {
	return &GetBatchHandler{usecases: uc, kind: kind}
}

func (h *GetBatchHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/" + string(h.kind.Target()) + "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: readCapability(h.kind),
	}
}

func (h *GetBatchHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.BatchIdParams{}}
}

func (h *GetBatchHandler) Execute(
	_ http.ResponseWriter, r *http.Request, input *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.BatchIdParams)
	batch, err := h.usecases.Get(r.Context(), h.kind, params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.BatchToJSON(batch)), nil
}

// ListRowsHandler: GET /imports/{kind}/{id}/rows — the row-by-row report.
//
// This is the dry run as a document: what will happen to each row of the
// customer's file, in their own row numbering, with the record the server
// resolved and the issues it raised. It is paged, and the page carries the
// total so a client can show "row 37 of 412" rather than "page 2".
type ListRowsHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
}

func NewListRowsHandler(uc domain.ImportUseCases, kind domain.Kind) *ListRowsHandler {
	return &ListRowsHandler{usecases: uc, kind: kind}
}

func (h *ListRowsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/" + string(h.kind.Target()) + "/{id}/rows",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: readCapability(h.kind),
		Paginated:  true,
	}
}

func (h *ListRowsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Params: dto.BatchIdParams{},
		Query:  dto.ListRowsQuery{},
	}
}

func (h *ListRowsHandler) Execute(
	_ http.ResponseWriter, r *http.Request, input *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.BatchIdParams)
	query, _ := input.Query.(*dto.ListRowsQuery)

	rows, total, err := h.usecases.Rows(r.Context(), h.kind, params.ID, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.RowsToJSON(rows)).
		WithPagination(types.Pagination{Total: total, Limit: query.Limit, Offset: query.Offset}), nil
}

// ListBatchesHandler: GET /imports/{kind} — the tenant's import history.
type ListBatchesHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
}

func NewListBatchesHandler(uc domain.ImportUseCases, kind domain.Kind) *ListBatchesHandler {
	return &ListBatchesHandler{usecases: uc, kind: kind}
}

func (h *ListBatchesHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/" + string(h.kind.Target()),
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: readCapability(h.kind),
		Paginated:  true,
	}
}

func (h *ListBatchesHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.ListImportsQuery{}}
}

func (h *ListBatchesHandler) Execute(
	_ http.ResponseWriter, r *http.Request, input *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ListImportsQuery)

	batches, total, err := h.usecases.List(r.Context(), h.kind, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.BatchesToJSON(batches)).
		WithPagination(types.Pagination{Total: total, Limit: query.Limit, Offset: query.Offset}), nil
}

// TemplateHandler: GET /imports/{kind}/template — the column set DESCRIBED.
//
// The product hands out its own header row rather than leaving it to be copied
// from a manual: the column names and the spellings accepted for each are
// server authority like everything else, and a customer who can fetch them can
// check an existing export against them without renaming anything first.
//
// This route answers JSON — names, whether each is required, the other
// spellings accepted, and one line of help — which is what a screen renders as
// a table. The blank SHEET is a different thing and lives next door at
// /imports/{kind}/template.csv: a description of columns saved under a
// spreadsheet name is not a spreadsheet, and handing one out as if it were is
// how a customer came to open 2 KB of JSON in Excel.
//
// It also publishes THE BOUNDS THIS INSTALLATION ENFORCES, for the same reason
// it publishes the columns. A screen that states a limit from a constant of its
// own is a second copy of a number nothing keeps honest, and the last one was
// 25 MB against a server that refuses at 2 MiB: a customer was promised twelve
// times what the product would accept, and the refusal they got named no size
// at all. The limits are configuration (config.ImportConfig), an operator may
// raise them, and a self-hosted installation may differ from the hosted one —
// so the answer to "what will fit" can only come from the server that decides
// it.
type TemplateHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
	bounds   Bounds
}

func NewTemplateHandler(uc domain.ImportUseCases, kind domain.Kind, bounds Bounds) *TemplateHandler {
	return &TemplateHandler{usecases: uc, kind: kind, bounds: bounds}
}

// templateResponse is dto.TemplateJSON with the bounds beside it. The embedded
// struct inlines its own fields, so `kind` and `fields` are where they have
// always been and `limits` is additive.
type templateResponse struct {
	dto.TemplateJSON
	Limits limitsJSON `json:"limits"`
}

// limitsJSON is what an upload must fit inside, as numbers rather than as
// prose: the client states them in its own words and checks a chosen file
// against them before spending an upload that cannot succeed.
type limitsJSON struct {
	// MaxFileBytes is the same number readUpload enforces and tooLarge names.
	MaxFileBytes int64 `json:"maxFileBytes"`
	// MaxRows counts DATA rows, the header excluded — the unit tooManyRows
	// refuses in, and the one the customer's own sheet is measured in.
	MaxRows int `json:"maxRows"`
}

func (h *TemplateHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/" + string(h.kind.Target()) + "/template",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: readCapability(h.kind),
	}
}

func (h *TemplateHandler) Execute(
	_ http.ResponseWriter, r *http.Request, _ *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	return httpkit.Ok(templateResponse{
		TemplateJSON: dto.TemplateJSON{
			Kind:   string(h.kind),
			Fields: h.usecases.Template(r.Context(), h.kind),
		},
		Limits: limitsJSON{
			MaxFileBytes: h.bounds.MaxFileBytes,
			MaxRows:      h.bounds.MaxRows,
		},
	}), nil
}

// ---- the blank sheet ---------------------------------------------------------

// templateContentType is what a spreadsheet program is told it is receiving.
// Delimited text rather than a workbook: it opens in every spreadsheet program
// and in every text editor, and a customer editing one column of it in `vi` is
// a support call that never happens.
const templateContentType = "text/csv; charset=utf-8"

// TemplateSheetHandler: GET /imports/{kind}/template.csv — the empty sheet.
//
// GENERATED from the same domain.Fields() the parser binds against, every time
// it is asked for, never a file kept beside the code. A static sheet is a
// second column list, and a second column list drifts from the first — which is
// exactly the failure this route exists to end: the wizard's own hardcoded list
// had gone eight columns out of date, and every deadline column with it.
//
// The header row is each field's canonical Name.
// domain.TestEveryFieldNameBindsAsItsOwnHeader pins those names as spellings
// the parser accepts, so the sheet the product hands out is provably a sheet
// the product can read back. There are no example rows: a blank sheet is what
// was asked for, and an invented row is a value somebody eventually commits.
type TemplateSheetHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
}

func NewTemplateSheetHandler(uc domain.ImportUseCases, kind domain.Kind) *TemplateSheetHandler {
	return &TemplateSheetHandler{usecases: uc, kind: kind}
}

func (h *TemplateSheetHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/" + string(h.kind.Target()) + "/template.csv",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: readCapability(h.kind),
	}
}

// Execute writes the response itself and returns (nil, nil): the body is a
// sheet, not an envelope, and nothing about it belongs inside {status, data}.
func (h *TemplateSheetHandler) Execute(
	w http.ResponseWriter, r *http.Request, _ *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	sheet := TemplateSheet(h.usecases.Template(r.Context(), h.kind))

	hdr := w.Header()
	if requestID := app.GetRequestId(r.Context()); requestID != "" {
		hdr.Set("X-Request-Id", requestID)
	}
	hdr.Set("Content-Type", templateContentType)
	hdr.Set("Content-Length", strconv.Itoa(len(sheet)))
	hdr.Set("Content-Disposition", `attachment; filename="`+TemplateFileName(h.kind)+`"`)
	hdr.Set("Cache-Control", "private, no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sheet)
	return nil, nil //nolint:nilnil // (nil, nil) = the handler wrote the response
}

// TemplateSheet renders the header row of a blank import sheet: one column per
// field, in Fields() order, written through encoding/csv so a field name that
// ever grows a comma or a quote is quoted rather than splitting a column.
//
// CRLF because RFC 4180 says so and because the sheet is opened on Windows more
// often than not; every reader this product has accepts either.
//
// It OPENS WITH THE UTF-8 BYTE-ORDER MARK, and that is the whole point of these
// three bytes. Every heading here is ASCII, so the mark buys the sheet as
// downloaded nothing — it is for the sheet as SENT BACK. A spreadsheet decides
// how to save a CSV from how it read it, and a file that declares nothing is
// read, and then written, in the machine's own ANSI codepage. The customer then
// returns a file whose encoding is a property of their laptop: a name typed as
// `Łódź Spółka` on a Central-European Windows machine comes back as Windows-1250
// bytes, the reader's fallback guesses Windows-1252 (it cannot do better — the
// file says nothing), and the name is read as `£ódŸ Spó³ka`. That is a value
// quietly changed, which is the one outcome this module refuses to produce, and
// no amount of care on the reading side can undo it: by then the evidence is
// gone. Declaring the encoding on the way OUT is what keeps it on the way back,
// because a spreadsheet that opened a marked file saves a marked file.
//
// The Content-Type's `charset=utf-8` does not do this job: it describes the
// response, and is gone the moment the bytes are a file on disk.
func TemplateSheet(fields []domain.Field) []byte {
	header := make([]string, 0, len(fields))
	for _, f := range fields {
		header = append(header, f.Name)
	}

	var buf bytes.Buffer
	buf.WriteString(utf8BOM)
	out := csv.NewWriter(&buf)
	out.UseCRLF = true
	_ = out.Write(header) // a bytes.Buffer cannot fail
	out.Flush()
	return buf.Bytes()
}

// utf8BOM is U+FEFF encoded as UTF-8: the mark a spreadsheet reads as "this
// file is UTF-8". parsing.decodeText consumes it on the way back in, so a sheet
// returned untouched parses with no trace of it.
const utf8BOM = "\uFEFF"

// TemplateFileName is the name the sheet is offered under. It names the product
// and what the sheet is for, because it lands in a downloads folder beside a
// year of other people's spreadsheets.
func TemplateFileName(kind domain.Kind) string {
	return "zentax-" + strings.ReplaceAll(string(kind.Target()), " ", "-") + "-template.csv"
}
