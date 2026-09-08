package handlers

import (
	"reflect"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/swagger"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/documents/dto"
)

// year is repeatable: the spec describes it as an array of strings with an
// ARRAY example.
func TestOpenAPI_YearIsAnArray(t *testing.T) {
	h := NewListDocumentsHandler(nil)
	schema := h.DefineSchema()
	spec := swagger.Generate([]routing.RouteMeta{{
		Definition: h.DefineRoute(), Schema: &schema, FullPath: "/documents",
	}}, types.ModeExternal, swagger.Config{Title: "Test API"})
	op := spec.Paths["/documents"]["get"]
	if op == nil {
		t.Fatal("no GET /documents operation in the spec")
	}
	for _, p := range op.Parameters {
		if p.Name != "year" {
			continue
		}
		if p.Schema.Type != "array" || p.Schema.Items == nil || p.Schema.Items.Type != "string" {
			t.Fatalf("year must be an array of strings, got %+v", p.Schema)
		}
		if l := p.Schema.Items.MaxLength; l == nil || *l != 9 {
			t.Fatalf("each year is capped at 9, got %v", l)
		}
		if ex, ok := p.Example.([]string); !ok || !reflect.DeepEqual(ex, []string{"2025"}) {
			t.Fatalf("year example must be an array, got %#v", p.Example)
		}
		return
	}
	t.Fatal("year parameter not described")
}

// Filters drops "" / "all" and keeps the rest (the `none` sentinel included)
// in order; nil when nothing remains.
func TestFilters(t *testing.T) {
	if got := dto.Filters([]string{"all", "", "2025", "none"}); !reflect.DeepEqual(got, []string{"2025", "none"}) {
		t.Fatalf("got %v", got)
	}
	if got := dto.Filters([]string{"all"}); got != nil {
		t.Fatalf("\"all\" alone must be no filter, got %v", got)
	}
}
