package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// ExportRawHandler serves GET /reports/export-raw: one of three flat datasets
// (workflows | tasks | tax-data) with exactly the column keys the export page
// declares, as a capped page (default 1000, max 5000) with an exact
// totalCount (ADR-0021 rule 2). Every workflow status takes part.
type ExportRawHandler struct {
	reader domain.Reader
}

func NewExportRawHandler(reader domain.Reader) *ExportRawHandler {
	return &ExportRawHandler{reader: reader}
}

func (h *ExportRawHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/export-raw",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskRead,
	}
}

func (h *ExportRawHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.ExportRawQuery{}}
}

func (h *ExportRawHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ExportRawQuery)

	category, err := optionalEnum("category", query.Category, "recurring", "project")
	if err != nil {
		return nil, err
	}
	args := domain.ExportArgs{
		Category: category,
		Limit:    query.Limit,
		Offset:   query.Offset,
	}
	if args.EntityID, err = optionalID("entityId", query.EntityID); err != nil {
		return nil, err
	}
	if args.ObligationTypeID, err = optionalID("obligationTypeId", query.ObligationTypeID); err != nil {
		return nil, err
	}
	if args.DateFrom, err = optionalDate("dateFrom", query.DateFrom); err != nil {
		return nil, err
	}
	if args.DateTo, err = optionalDate("dateTo", query.DateTo); err != nil {
		return nil, err
	}

	ctx := r.Context()
	var rows []map[string]any
	var total int
	switch query.Dataset {
	case domain.DatasetWorkflows:
		var wf []domain.ExportWorkflowRow
		if wf, total, err = h.reader.ExportWorkflows(ctx, args); err == nil {
			rows = dto.ExportWorkflowsToJSON(wf)
		}
	case domain.DatasetTaxData:
		var td []domain.ExportTaxDataRow
		if td, total, err = h.reader.ExportTaxData(ctx, args); err == nil {
			rows = dto.ExportTaxDataToJSON(td)
		}
	default:
		var tasks []domain.ExportTaskRow
		if tasks, total, err = h.reader.ExportTasks(ctx, args); err == nil {
			rows = dto.ExportTasksToJSON(tasks)
		}
	}
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.ExportToJSON(query.Dataset, rows, total)), nil
}
