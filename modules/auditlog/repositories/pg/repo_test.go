package pg

import (
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// unscoped is the tenant-wide reader: the WHERE clause must be exactly what it
// was before read scope existed.
var unscoped = authz.UnboundedReadScope()

func ptr(s string) *string { return &s }

func date(t *testing.T, s string) *dateonly.Date {
	t.Helper()
	d, err := dateonly.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return &d
}

// The WHERE clause is assembled from the filters that are SET — never a
// generic `($n IS NULL OR col = $n)` shape.
func TestAuditWhere_OnlySetFiltersBecomePredicates(t *testing.T) {
	where, args := auditWhere(domain.ListArgs{}, unscoped)
	if where != "" || len(args) != 0 {
		t.Fatalf("no filter must render no WHERE, got %q with %v", where, args)
	}

	where, args = auditWhere(domain.ListArgs{
		WorkflowID:   ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		ResourceType: ptr("workflow"),
		ResourceID:   ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		Actions:      []string{"workflow.started"},
		From:         date(t, "2026-01-01"),
		To:           date(t, "2026-01-31"),
	}, unscoped)
	want := "\nWHERE rw.id = $1::uuid" +
		"\n  AND a.resource_type = $2::varchar" +
		"\n  AND a.resource_id = $3::uuid" +
		"\n  AND a.action = $4::varchar" +
		"\n  AND a.occurred_at >= ($5::date)::timestamp AT TIME ZONE 'UTC'" +
		"\n  AND a.occurred_at < ($6::date + 1)::timestamp AT TIME ZONE 'UTC'"
	if where != want {
		t.Fatalf("got %q\nwant %q", where, want)
	}
	if len(args) != 6 || args[3] != "workflow.started" || args[4] != "2026-01-01" || args[5] != "2026-01-31" {
		t.Fatalf("binds must follow the predicates in order, got %v", args)
	}
	if strings.Contains(where, "IS NULL OR $") {
		t.Fatalf("a NULL-tolerant predicate must never be emitted:\n%s", where)
	}

	// A subset binds from $1 again.
	where, args = auditWhere(domain.ListArgs{To: date(t, "2026-01-31")}, unscoped)
	if where != "\nWHERE a.occurred_at < ($1::date + 1)::timestamp AT TIME ZONE 'UTC'" || len(args) != 1 {
		t.Fatalf("got %q with %v", where, args)
	}
}

// action is a set: one value is an equality (the single-value contract keeps
// working), several become `= ANY(array)` with ONE bind carrying them all.
func TestAuditWhere_ActionsAreASet(t *testing.T) {
	where, args := auditWhere(domain.ListArgs{Actions: []string{"workflow.started", "workflow.created"}}, unscoped)
	if where != "\nWHERE a.action = ANY($1::varchar[])" {
		t.Fatalf("got %q", where)
	}
	set, ok := args[0].([]string)
	if !ok || len(set) != 2 || set[0] != "workflow.started" || set[1] != "workflow.created" {
		t.Fatalf("the array bind must carry every action, got %v", args)
	}

	where, args = auditWhere(domain.ListArgs{Actions: []string{"entity.created"}}, unscoped)
	if where != "\nWHERE a.action = $1::varchar" || args[0] != "entity.created" {
		t.Fatalf("got %q with %v", where, args)
	}
}

// A narrowed reader's WHERE carries the scope predicate, after the filters and
// bound ONCE: an audit row reaches its entity four ways (through the resolved
// workflow, as the entity itself, through its entity obligation, through its
// document's workflow) and every branch must reuse the one uuid[] parameter.
func TestAuditWhere_NarrowedScopeIsOneBoundSetOverEveryResourceFamily(t *testing.T) {
	ids := []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8", "6ba7b810-9dad-11d1-80b4-00c04fd430c9"}
	scope := authz.NarrowedReadScope(ids...)

	where, args := auditWhere(domain.ListArgs{}, scope)
	if len(args) != 1 {
		t.Fatalf("the entity set must be bound exactly once, got %v", args)
	}
	bound, ok := args[0].([]string)
	if !ok || len(bound) != 2 || bound[0] != ids[0] || bound[1] != ids[1] {
		t.Fatalf("the bind must carry the whole readable set, got %v", args)
	}
	if n := strings.Count(where, "$1::uuid[]"); n != 4 {
		t.Fatalf("every resource family must reuse $1, got %d branches in:\n%s", n, where)
	}
	for _, want := range []string{
		"w.entity_id = ANY($1::uuid[])",
		"a.resource_type = 'entity' AND a.resource_id = ANY($1::uuid[])",
		"a.resource_type = 'entity_obligation' AND EXISTS (SELECT 1 FROM entity_obligations seo",
		"a.resource_type = 'document' AND EXISTS (SELECT 1 FROM documents sd JOIN workflows sw",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("missing branch %q in:\n%s", want, where)
		}
	}

	// The filters keep their own bind numbering: the scope is appended last.
	where, args = auditWhere(domain.ListArgs{ResourceType: ptr("workflow")}, scope)
	if !strings.HasPrefix(where, "\nWHERE a.resource_type = $1::varchar\n  AND (w.entity_id = ANY($2::uuid[])") {
		t.Fatalf("the scope predicate must follow the filters, got %q", where)
	}
	if len(args) != 2 || args[0] != "workflow" {
		t.Fatalf("got %v", args)
	}
}

// A principal whose grants carry audit:read on nothing readable reads nothing —
// never everything — and the FALSE is a predicate, so the count narrows too.
func TestAuditWhere_EmptyScopeReadsNothing(t *testing.T) {
	where, args := auditWhere(domain.ListArgs{}, authz.NarrowedReadScope())
	if where != "\nWHERE FALSE" || len(args) != 0 {
		t.Fatalf("an empty read scope must read nothing, got %q with %v", where, args)
	}
}

// The page statement ends in the unique per-tenant seq, so offset pages are
// stable; the count aggregates over the same FROM (+ WHERE) as the page.
func TestAuditSQL_Shape(t *testing.T) {
	if !strings.HasSuffix(strings.TrimSpace(auditOrder), "ORDER BY a.seq DESC") {
		t.Fatalf("the trail has one order, seq DESC: %q", auditOrder)
	}
	if !strings.HasSuffix(auditSelect, auditFrom) || !strings.HasSuffix(auditCount, auditFrom) {
		t.Fatalf("page and count must share the FROM so the total is exact")
	}
	if strings.Contains(auditFrom, "WHERE") {
		t.Fatalf("the FROM must carry no static predicate; filters are per request:\n%s", auditFrom)
	}
}
