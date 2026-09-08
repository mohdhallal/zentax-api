package handlers

import (
	"reflect"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/swagger"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// action is repeatable: the spec describes it as an array of strings with an
// ARRAY example (a bare string on an array schema trips spec linters).
func TestOpenAPI_ActionIsAnArray(t *testing.T) {
	h := NewListAuditLogHandler(nil)
	schema := h.DefineSchema()
	spec := swagger.Generate([]routing.RouteMeta{{
		Definition: h.DefineRoute(), Schema: &schema, FullPath: "/audit-log",
	}}, types.ModeExternal, swagger.Config{Title: "Test API"})
	op := spec.Paths["/audit-log"]["get"]
	if op == nil {
		t.Fatal("no GET /audit-log operation in the spec")
	}
	for _, p := range op.Parameters {
		if p.Name != "action" {
			continue
		}
		if p.Schema.Type != "array" || p.Schema.Items == nil || p.Schema.Items.Type != "string" {
			t.Fatalf("action must be an array of strings, got %+v", p.Schema)
		}
		if l := p.Schema.Items.MaxLength; l == nil || *l != 60 {
			t.Fatalf("each action is capped at 60, got %v", l)
		}
		if ex, ok := p.Example.([]string); !ok || !reflect.DeepEqual(ex, []string{"workflow.started"}) {
			t.Fatalf("action example must be an array, got %#v", p.Example)
		}
		return
	}
	t.Fatal("action parameter not described")
}

// actions keeps the order and drops blanks; nil when nothing remains.
func TestActions(t *testing.T) {
	if got := actions([]string{"", "workflow.started", "", "workflow.created"}); !reflect.DeepEqual(got, []string{"workflow.started", "workflow.created"}) {
		t.Fatalf("got %v", got)
	}
	if got := actions([]string{""}); got != nil {
		t.Fatalf("blank alone must be no filter, got %v", got)
	}
	if got := actions(nil); got != nil {
		t.Fatalf("got %v", got)
	}
}
