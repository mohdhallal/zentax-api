package pg

import (
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

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
	where, args := auditWhere(domain.ListArgs{})
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
	})
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
	where, args = auditWhere(domain.ListArgs{To: date(t, "2026-01-31")})
	if where != "\nWHERE a.occurred_at < ($1::date + 1)::timestamp AT TIME ZONE 'UTC'" || len(args) != 1 {
		t.Fatalf("got %q with %v", where, args)
	}
}

// action is a set: one value is an equality (the single-value contract keeps
// working), several become `= ANY(array)` with ONE bind carrying them all.
func TestAuditWhere_ActionsAreASet(t *testing.T) {
	where, args := auditWhere(domain.ListArgs{Actions: []string{"workflow.started", "workflow.created"}})
	if where != "\nWHERE a.action = ANY($1::varchar[])" {
		t.Fatalf("got %q", where)
	}
	set, ok := args[0].([]string)
	if !ok || len(set) != 2 || set[0] != "workflow.started" || set[1] != "workflow.created" {
		t.Fatalf("the array bind must carry every action, got %v", args)
	}

	where, args = auditWhere(domain.ListArgs{Actions: []string{"entity.created"}})
	if where != "\nWHERE a.action = $1::varchar" || args[0] != "entity.created" {
		t.Fatalf("got %q with %v", where, args)
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
