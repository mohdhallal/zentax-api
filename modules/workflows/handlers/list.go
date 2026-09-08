package handlers

import (
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type ListWorkflowsHandler struct {
	usecases domain.WorkflowUseCases
}

func NewListWorkflowsHandler(uc domain.WorkflowUseCases) *ListWorkflowsHandler {
	return &ListWorkflowsHandler{usecases: uc}
}

func (h *ListWorkflowsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowRead,
		Paginated:  true,
	}
}

func (h *ListWorkflowsHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListWorkflowsQuery{},
	}
}

// DefineSortColumns maps the public sort fields to the `w.`-qualified columns
// of the joined list select (see repositories/pg/sql.go).
func (h *ListWorkflowsHandler) DefineSortColumns() map[string]string {
	return map[string]string{
		"createdAt": "w.created_at",
		"name":      "w.name",
	}
}

func (h *ListWorkflowsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	// Year filters are lenient everywhere: a blank or the legacy "all" is "no
	// filter" (the reports feed, task-summary, workflow-stats and documents all
	// do this), so the UI's select can send its "All years" value verbatim.
	args := dropFilterValues(*input.Pagination, "w.financial_year", "", "all")
	result, err := h.usecases.List(r.Context(), args)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, dto.WorkflowToJSON(&result.Items[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  result.Total,
		Limit:  input.Pagination.Limit,
		Offset: input.Pagination.Offset,
	}), nil
}

// dropFilterValues removes the given values from a repeatable filter's value
// set; when nothing is left the filter itself is dropped. Single-value forms
// are handled the same way.
func dropFilterValues(args sharedtypes.ListArgs, column string, drop ...string) sharedtypes.ListArgs {
	isDropped := func(v string) bool {
		for _, d := range drop {
			if v == d {
				return true
			}
		}
		return false
	}
	kept := make([]sharedtypes.Filter, 0, len(args.Filters))
	for _, f := range args.Filters {
		if f.Column != column {
			kept = append(kept, f)
			continue
		}
		switch v := f.Value.(type) {
		case []string:
			vals := make([]string, 0, len(v))
			for _, x := range v {
				if !isDropped(x) {
					vals = append(vals, x)
				}
			}
			if len(vals) > 0 {
				kept = append(kept, sharedtypes.Filter{Column: f.Column, Value: vals})
			}
		case string:
			if !isDropped(v) {
				kept = append(kept, f)
			}
		default:
			kept = append(kept, f)
		}
	}
	args.Filters = kept
	return args
}
