package handlers

import (
	"reflect"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/swagger"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// queryParams generates the spec for one handler and indexes its query
// parameters by name.
func queryParams(t *testing.T, route types.Route, fullPath string) map[string]swagger.Parameter {
	t.Helper()
	schema := route.(types.RouteSchemaDefinition).DefineSchema()
	spec := swagger.Generate([]routing.RouteMeta{{
		Definition: route.DefineRoute(),
		Schema:     &schema,
		FullPath:   fullPath,
	}}, types.ModeExternal, swagger.Config{Title: "Test API"})
	op := spec.Paths[fullPath]["get"]
	if op == nil {
		t.Fatalf("no GET %s operation in the spec", fullPath)
	}
	out := map[string]swagger.Parameter{}
	for _, p := range op.Parameters {
		if p.In == "query" {
			out[p.Name] = p
		}
	}
	return out
}

// The feed's (and the summary's) new query parameters are described from the
// DTO tags: repeatable ones as arrays with an ARRAY example, enums with their
// values, the search with its length cap, the date bounds and the sort keys.
func TestOpenAPI_TaskFeedDescribesEveryFilter(t *testing.T) {
	feed := queryParams(t, NewTaskInstancesReportHandler(nil), "/reports/task-instances")
	summary := queryParams(t, NewTaskSummaryHandler(nil), "/reports/task-summary")

	for _, params := range []map[string]swagger.Parameter{feed, summary} {
		for _, name := range []string{
			"workflowId", "entityId", "assigneeId", "obligationTypeId", "taxType", "financialYear",
			"periodCode", "status", "workflowCategory", "due", "dueFrom", "dueTo", "search",
		} {
			if _, ok := params[name]; !ok {
				t.Fatalf("query parameter %q is not described", name)
			}
		}
		fy := params["financialYear"]
		if fy.Schema.Type != "array" || fy.Schema.Items == nil || fy.Schema.Items.Type != "string" {
			t.Fatalf("financialYear must be an array of strings, got %+v", fy.Schema)
		}
		if ex, ok := fy.Example.([]string); !ok || !reflect.DeepEqual(ex, []string{"2025"}) {
			t.Fatalf("financialYear example must be an array, got %#v", fy.Example)
		}
		if got := params["taxType"].Schema.Enum; !reflect.DeepEqual(got, []string{"VAT", "CIT", "TP", "WHT", "Custom"}) {
			t.Fatalf("taxType enum = %v", got)
		}
		if got := params["due"].Schema.Enum; !reflect.DeepEqual(got, []string{"overdue", "today", "thisWeek"}) {
			t.Fatalf("due enum = %v", got)
		}
		if got := params["status"].Schema.Enum; len(got) != 7 || got[0] != "open" {
			t.Fatalf("status enum must lead with the open pseudo-status, got %v", got)
		}
		if l := params["search"].Schema.MaxLength; l == nil || *l != 200 {
			t.Fatalf("search must be capped at 200, got %v", l)
		}
		if l := params["periodCode"].Schema.MaxLength; l == nil || *l != 16 {
			t.Fatalf("periodCode must be capped at 16, got %v", l)
		}
		if params["assigneeId"].Example != "me" {
			t.Fatalf("assigneeId example = %v", params["assigneeId"].Example)
		}
		if params["dueFrom"].Example != "2025-01-01" || params["dueTo"].Example != "2025-12-31" {
			t.Fatalf("date bound examples = %v / %v", params["dueFrom"].Example, params["dueTo"].Example)
		}
	}

	sort := feed["sort"]
	if sort.Schema.Type != "array" || sort.Schema.Items == nil {
		t.Fatalf("sort must be an array, got %+v", sort.Schema)
	}
	wantSort := []string{
		"dueDate:asc", "dueDate:desc", "createdAt:asc", "createdAt:desc", "status:asc", "status:desc",
		"workflow:asc", "workflow:desc", "entity:asc", "entity:desc", "name:asc", "name:desc",
	}
	if !reflect.DeepEqual(sort.Schema.Items.Enum, wantSort) {
		t.Fatalf("sort enum = %v, want %v", sort.Schema.Items.Enum, wantSort)
	}
	if _, ok := summary["sort"]; ok {
		t.Fatal("the summary takes no sort")
	}
	if _, ok := summary["limit"]; ok {
		t.Fatal("the summary takes no paging")
	}
}

// Every sort key the DTO admits has a repository column, and vice versa.
func TestTaskFeedSortKeys_MatchTheDTO(t *testing.T) {
	cols := NewTaskInstancesReportHandler(nil).DefineSortColumns()
	feed := queryParams(t, NewTaskInstancesReportHandler(nil), "/reports/task-instances")
	seen := map[string]bool{}
	for _, v := range feed["sort"].Schema.Items.Enum {
		key := v[:len(v)-4]
		if v[len(v)-5:] == ":desc" {
			key = v[:len(v)-5]
		}
		if _, ok := cols[key]; !ok {
			t.Fatalf("sort value %q has no column mapping", v)
		}
		seen[key] = true
	}
	for key := range cols {
		if !seen[key] {
			t.Fatalf("column mapping %q is not offered by the DTO", key)
		}
	}
}

// The workflow-stats filters are described too, financialYear as an array.
func TestOpenAPI_WorkflowStatsDescribesItsFilters(t *testing.T) {
	params := queryParams(t, NewWorkflowStatsHandler(nil), "/reports/workflow-stats")
	for _, name := range []string{"workflowId", "entityId", "financialYear", "status", "workflowCategory"} {
		if _, ok := params[name]; !ok {
			t.Fatalf("query parameter %q is not described", name)
		}
	}
	if params["financialYear"].Schema.Type != "array" {
		t.Fatalf("financialYear must be an array, got %+v", params["financialYear"].Schema)
	}
	if ex, ok := params["financialYear"].Example.([]string); !ok || !reflect.DeepEqual(ex, []string{"2025"}) {
		t.Fatalf("financialYear example must be an array, got %#v", params["financialYear"].Example)
	}
	if got := params["status"].Schema.Enum; !reflect.DeepEqual(got, []string{"draft", "active", "completed", "archived"}) {
		t.Fatalf("status enum = %v", got)
	}
}
