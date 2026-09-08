package pg

import (
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
)

func ptr(s string) *string { return &s }

// The view's WHERE always keeps soft-deleted documents out and otherwise
// carries a predicate ONLY for the filters that are set — never a generic
// `($n IS NULL OR col = $n)` shape — with the binds numbered in order.
func TestDocumentsWhere_OnlySetFiltersBecomePredicates(t *testing.T) {
	where, args := documentsWhere(domain.ListDocumentsFilter{}, nil)
	if where != "\nWHERE d.deleted_at IS NULL" || len(args) != 0 {
		t.Fatalf("no filter must render the live-only clause, got %q with %v", where, args)
	}

	where, args = documentsWhere(domain.ListDocumentsFilter{
		WorkflowID:     ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		TaskInstanceID: ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		EntityID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430ca"),
		DocumentType:   ptr("draft_return"),
		Years:          []string{"2025"},
		Search:         ptr("alpha"),
	}, nil)
	want := "\nWHERE d.deleted_at IS NULL" +
		"\n  AND d.workflow_id = $1::uuid" +
		"\n  AND d.task_instance_id = $2::uuid" +
		"\n  AND w.entity_id = $3::uuid" +
		"\n  AND d.document_type = $4::varchar" +
		"\n  AND w.financial_year = $5::varchar" +
		"\n  AND (v.file_name ILIKE $6::text ESCAPE '\\'" +
		"\n       OR d.label ILIKE $6::text ESCAPE '\\'" +
		"\n       OR d.notes ILIKE $6::text ESCAPE '\\')"
	if where != want {
		t.Fatalf("got %q\nwant %q", where, want)
	}
	if len(args) != 6 || args[3] != "draft_return" || args[4] != "2025" || args[5] != "%alpha%" {
		t.Fatalf("binds must follow the predicates in order, got %v", args)
	}
	if strings.Contains(where, "IS NULL OR $") {
		t.Fatalf("a NULL-tolerant predicate must never be emitted:\n%s", where)
	}

	// GetView narrows to one id, bound first.
	id := "6ba7b810-9dad-11d1-80b4-00c04fd430cb"
	where, args = documentsWhere(domain.ListDocumentsFilter{DocumentType: ptr("other")}, &id)
	if where != "\nWHERE d.deleted_at IS NULL\n  AND d.id = $1::uuid\n  AND d.document_type = $2::varchar" {
		t.Fatalf("got %q", where)
	}
	if len(args) != 2 || args[0] != id {
		t.Fatalf("binds = %v", args)
	}
}

// year is a set over the document's workflow: one value is an equality,
// several an ANY(array), `none` alone is IS NULL (a project workflow's
// documents), and `none` with values OR-s the two.
func TestDocumentsWhere_YearSet(t *testing.T) {
	prefix := "\nWHERE d.deleted_at IS NULL\n  AND "
	cases := []struct {
		years []string
		want  string
		args  int
	}{
		{nil, "\nWHERE d.deleted_at IS NULL", 0},
		{[]string{"2025"}, prefix + "w.financial_year = $1::varchar", 1},
		{[]string{"2024", "2025"}, prefix + "w.financial_year = ANY($1::varchar[])", 1},
		{[]string{domain.FinancialYearNone}, prefix + "w.financial_year IS NULL", 0},
		{[]string{"2025", domain.FinancialYearNone}, prefix + "(w.financial_year IS NULL OR w.financial_year = $1::varchar)", 1},
		{[]string{domain.FinancialYearNone, "2024", "2025"},
			prefix + "(w.financial_year IS NULL OR w.financial_year = ANY($1::varchar[]))", 1},
	}
	for _, tc := range cases {
		where, args := documentsWhere(domain.ListDocumentsFilter{Years: tc.years}, nil)
		if where != tc.want {
			t.Fatalf("%v: got %q, want %q", tc.years, where, tc.want)
		}
		if len(args) != tc.args {
			t.Fatalf("%v: got %d binds, want %d", tc.years, len(args), tc.args)
		}
	}
	_, args := documentsWhere(domain.ListDocumentsFilter{Years: []string{"2024", domain.FinancialYearNone, "2025"}}, nil)
	years, ok := args[0].([]string)
	if !ok || len(years) != 2 || years[0] != "2024" || years[1] != "2025" {
		t.Fatalf("array bind must hold the plain years only, got %v", args)
	}
}

// search is a literal, case-insensitive substring: trimmed, the LIKE
// metacharacters escaped, ONE bind shared by the three columns; blank is no
// search.
func TestDocumentsWhere_SearchIsEscapedAndLiteral(t *testing.T) {
	_, args := documentsWhere(domain.ListDocumentsFilter{Search: ptr(`  100% reconciled_ok\  `)}, nil)
	if len(args) != 1 || args[0] != `%100\% reconciled\_ok\\%` {
		t.Fatalf("pattern = %v", args)
	}
	for _, term := range []string{"", "  ", "\t"} {
		where, args := documentsWhere(domain.ListDocumentsFilter{Search: ptr(term)}, nil)
		if where != "\nWHERE d.deleted_at IS NULL" || len(args) != 0 {
			t.Fatalf("%q: got %q with %v", term, where, args)
		}
	}
}

// The page order ends in the unique document id in the primary direction, and
// the FROM carries no static predicate (page and count share the WHERE).
func TestDocumentsSQL_Shape(t *testing.T) {
	if viewOrder != " ORDER BY d.created_at DESC, d.id DESC" {
		t.Fatalf("order = %q", viewOrder)
	}
	if strings.Contains(viewFrom, "WHERE") {
		t.Fatalf("the FROM must carry no static predicate:\n%s", viewFrom)
	}
	if !strings.HasSuffix(viewSelect, viewFrom) || !strings.HasSuffix(viewCount, viewFrom) {
		t.Fatalf("page and count must share the FROM so the total is exact")
	}
}
