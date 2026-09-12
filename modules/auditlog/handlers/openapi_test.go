package handlers

import (
	"reflect"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/swagger"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/auditlog/dto"
)

// spec renders this one route's OpenAPI operation.
func spec(t *testing.T) *swagger.Operation {
	t.Helper()
	h := NewListAuditLogHandler(nil)
	schema := h.DefineSchema()
	doc := swagger.Generate([]routing.RouteMeta{{
		Definition: h.DefineRoute(), Schema: &schema, FullPath: "/audit-log",
	}}, types.ModeExternal, swagger.Config{Title: "Test API"})
	op := doc.Paths["/audit-log"]["get"]
	if op == nil {
		t.Fatal("no GET /audit-log operation in the spec")
	}
	return op
}

// The published contract advertises EVERY resource type the trail records
// (dto.ResourceTypes) — a generated client, and the auditor reading the spec,
// must see that a token, a service account or the tenant record can be asked
// for. dto's own tests pin that list to the Record call sites; this one pins
// the spec to the list, so the whole path from writer to contract is checked.
func TestOpenAPI_ResourceTypeEnumIsTheWholeVocabulary(t *testing.T) {
	for _, p := range spec(t).Parameters {
		if p.Name != "resourceType" {
			continue
		}
		if !reflect.DeepEqual(p.Schema.Enum, dto.ResourceTypes) {
			t.Fatalf("the spec's resourceType enum is not dto.ResourceTypes.\n got: %v\nwant: %v",
				p.Schema.Enum, dto.ResourceTypes)
		}
		return
	}
	t.Fatal("resourceType parameter not described")
}

// action is repeatable: the spec describes it as an array of strings with an
// ARRAY example (a bare string on an array schema trips spec linters).
func TestOpenAPI_ActionIsAnArray(t *testing.T) {
	for _, p := range spec(t).Parameters {
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
