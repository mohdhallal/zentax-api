package swagger

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// TestPaginatedEnvelopeMirrorsPaginationType pins the pagination block of the
// PaginatedEnvelope component to types.Pagination: every json field the
// server writes (total, limit, offset, hasMore) is described and required, so
// the UI's regenerated types see hasMore.
func TestPaginatedEnvelopeMirrorsPaginationType(t *testing.T) {
	spec := Generate(nil, types.ModeExternal, Config{Title: "Test API"})

	env, ok := spec.Components.Schemas["PaginatedEnvelope"]
	if !ok {
		t.Fatal("PaginatedEnvelope component schema missing")
	}
	pg, ok := env.Properties["pagination"]
	if !ok {
		t.Fatal("PaginatedEnvelope.pagination missing")
	}

	var want []string
	pt := reflect.TypeOf(types.Pagination{})
	for i := 0; i < pt.NumField(); i++ {
		want = append(want, strings.SplitN(pt.Field(i).Tag.Get("json"), ",", 2)[0])
	}
	sort.Strings(want)

	var got []string
	for name := range pg.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pagination properties = %v, want the types.Pagination fields %v", got, want)
	}

	required := append([]string(nil), pg.Required...)
	sort.Strings(required)
	if !reflect.DeepEqual(required, want) {
		t.Fatalf("pagination required = %v, want every field %v", required, want)
	}

	if pg.Properties["hasMore"].Type != schemaTypeBoolean {
		t.Fatalf("hasMore must be a boolean, got %q", pg.Properties["hasMore"].Type)
	}
	for _, name := range []string{"total", "limit", "offset"} {
		if pg.Properties[name].Type != schemaTypeInteger {
			t.Fatalf("%s must be an integer, got %q", name, pg.Properties[name].Type)
		}
	}
}

// TestRepeatedQueryParamsDescribedAsExplodedArrays: a []string query field
// (the multi-value filters: ?status=active&status=draft) is an exploded form
// array whose items carry the per-element (post-dive) enum.
func TestRepeatedQueryParamsDescribedAsExplodedArrays(t *testing.T) {
	type query struct {
		Limit         int      `json:"limit"         default:"20" validate:"min=1,max=100"`
		Status        []string `json:"status"        filter:"status" validate:"omitempty,dive,oneof=draft active completed archived" example:"active"`
		FinancialYear []string `json:"financialYear" filter:"financial_year" validate:"omitempty,dive,max=9" example:"2025"`
		EntityID      *string  `json:"entityId"      filter:"entity_id" validate:"omitempty,uuid"`
	}
	spec := Generate([]routing.RouteMeta{{
		FullPath:   "/workflows",
		Definition: types.RouteDefinition{Method: http.MethodGet, Path: "/", Tenant: true, Paginated: true},
		Schema:     &types.SchemaDefinition{Query: query{}},
	}}, types.ModeExternal, Config{Title: "Test API"})

	params := map[string]Parameter{}
	for _, p := range spec.Paths["/workflows"]["get"].Parameters {
		params[p.Name] = p
	}

	status := params["status"]
	if status.In != "query" || status.Schema.Type != schemaTypeArray || status.Schema.Items == nil {
		t.Fatalf("status must be a query array parameter: %#v", status)
	}
	if status.Style != "form" || status.Explode == nil || !*status.Explode {
		t.Fatalf("status must be style=form, explode=true so it repeats as ?status=a&status=b: %#v", status)
	}
	if !reflect.DeepEqual(status.Schema.Items.Enum, []string{"draft", "active", "completed", "archived"}) {
		t.Fatalf("status items must carry the post-dive enum, got %v", status.Schema.Items.Enum)
	}
	if status.Required {
		t.Fatal("an omitempty filter is optional")
	}

	fy := params["financialYear"]
	if fy.Schema.Type != schemaTypeArray || fy.Schema.Items == nil || fy.Schema.Items.Type != schemaTypeString {
		t.Fatalf("financialYear must be an array of strings: %#v", fy)
	}
	if fy.Schema.Items.MaxLength == nil || *fy.Schema.Items.MaxLength != 9 {
		t.Fatalf("financialYear items must carry max length 9: %#v", fy.Schema.Items)
	}

	// A single-value filter is unchanged by the multi-value support.
	entity := params["entityId"]
	if entity.Schema.Type != schemaTypeString || entity.Schema.Format != "uuid" {
		t.Fatalf("entityId must stay a uuid string: %#v", entity)
	}
}
