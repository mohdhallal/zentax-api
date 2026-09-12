package pg

import (
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func date(t *testing.T, s string) *dateonly.Date {
	t.Helper()
	d, err := dateonly.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return &d
}

// Each of the increment-4 filters emits its predicate ONLY when set, with the
// binds numbered in order of appearance — and nothing else leaks in.
func TestTaskFilterWhere_EachNewFilterEmitsOnlyWhenSet(t *testing.T) {
	cases := []struct {
		name  string
		f     domain.TaskFilters
		want  string
		binds []any
	}{
		{"obligationTypeId", domain.TaskFilters{ObligationTypeID: ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8")},
			"\nWHERE w.obligation_type_id = $1::uuid", []any{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}},
		{"taxType is a semi-join on the template", domain.TaskFilters{TaxType: ptr("CIT")},
			"\nWHERE w.obligation_type_id IN (SELECT sot.id FROM obligation_types sot WHERE sot.template = $1::varchar)", []any{"CIT"}},
		{"periodCode", domain.TaskFilters{PeriodCode: ptr("M12")},
			"\nWHERE ti.period_code = $1::varchar", []any{"M12"}},
		{"unassigned is IS NULL, no bind", domain.TaskFilters{AssigneeID: ptr(domain.AssigneeUnassigned)},
			"\nWHERE ti.assignee_id IS NULL", nil},
		{"due=overdue is the summary's own predicate", domain.TaskFilters{Due: ptr(domain.DueOverdue)},
			"\nWHERE (" + dueOverdue + ")", nil},
		{"due=today", domain.TaskFilters{Due: ptr(domain.DueToday)},
			"\nWHERE (" + dueToday + ")", nil},
		{"due=thisWeek", domain.TaskFilters{Due: ptr(domain.DueThisWeek)},
			"\nWHERE (" + dueThisWeek + ")", nil},
		{"dueFrom is inclusive", domain.TaskFilters{DueFrom: date(t, "2025-02-10")},
			"\nWHERE ti.due_date >= $1::date", []any{"2025-02-10"}},
		{"dueTo is inclusive", domain.TaskFilters{DueTo: date(t, "2025-03-02")},
			"\nWHERE ti.due_date <= $1::date", []any{"2025-03-02"}},
		{"both bounds", domain.TaskFilters{DueFrom: date(t, "2025-02-10"), DueTo: date(t, "2025-03-02")},
			"\nWHERE ti.due_date >= $1::date\n  AND ti.due_date <= $2::date", []any{"2025-02-10", "2025-03-02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := taskFilterWhere(unscoped, tc.f)
			if where != tc.want {
				t.Fatalf("got %q, want %q", where, tc.want)
			}
			if len(args) != len(tc.binds) {
				t.Fatalf("got %d binds %v, want %v", len(args), args, tc.binds)
			}
			for i := range tc.binds {
				if args[i] != tc.binds[i] {
					t.Fatalf("bind %d = %v, want %v", i+1, args[i], tc.binds[i])
				}
			}
			if strings.Contains(where, "IS NULL OR $") {
				t.Fatalf("a NULL-tolerant predicate must never be emitted:\n%s", where)
			}
		})
	}
}

// An unknown due window (impossible past the DTO's enum) emits nothing
// rather than an unguarded predicate.
func TestTaskFilterWhere_UnknownDueWindowIsIgnored(t *testing.T) {
	where, args := taskFilterWhere(unscoped, domain.TaskFilters{Due: ptr("someday")})
	if where != "" || len(args) != 0 {
		t.Fatalf("got %q with %v", where, args)
	}
}

// The due windows only ever count OPEN work against the tenant's day — the
// same constants the summary tiles use, so a tile and its drill-down agree.
func TestDueWindows_AreOpenWorkAgainstTheTenantDay(t *testing.T) {
	for name, pred := range map[string]string{"overdue": dueOverdue, "today": dueToday, "thisWeek": dueThisWeek} {
		if !strings.HasPrefix(pred, taskOpen+" AND ") {
			t.Fatalf("%s must be guarded by the open predicate: %s", name, pred)
		}
		if !strings.Contains(pred, tenantToday) {
			t.Fatalf("%s must be evaluated against the tenant's day: %s", name, pred)
		}
		if strings.Contains(strings.ToUpper(pred), "CURRENT_DATE") || strings.Contains(strings.ToUpper(pred), "NOW()::DATE") {
			t.Fatalf("%s must never read the server's day: %s", name, pred)
		}
	}
	if !strings.Contains(dueThisWeek, tenantWeekEnd) {
		t.Fatalf("thisWeek must end on the tenant's week end: %s", dueThisWeek)
	}
}

// search is a literal, case-insensitive substring: the term is trimmed, the
// LIKE metacharacters (and the escape character) are escaped, and the ONE
// bound pattern is matched against the instance's own text, its workflow's
// name and — through semi-joins, never a display LEFT JOIN — the names of its
// entity and obligation type.
func TestTaskFilterWhere_SearchIsEscapedAndLiteral(t *testing.T) {
	where, args := taskFilterWhere(unscoped, domain.TaskFilters{Search: ptr(`  50% off_the\top  `)})
	if len(args) != 1 {
		t.Fatalf("search binds exactly one pattern, got %v", args)
	}
	if args[0] != `%50\% off\_the\\top%` {
		t.Fatalf("pattern = %q", args[0])
	}
	for _, want := range []string{
		"ti.name ILIKE $1::text ESCAPE '\\'",
		"ti.period_code ILIKE $1::text ESCAPE '\\'",
		"w.name ILIKE $1::text ESCAPE '\\'",
		"EXISTS (SELECT 1 FROM entities se WHERE se.id = w.entity_id AND se.name ILIKE $1::text ESCAPE '\\')",
		"EXISTS (SELECT 1 FROM obligation_types sot WHERE sot.id = w.obligation_type_id AND sot.name ILIKE $1::text ESCAPE '\\')",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("search lacks %q:\n%s", want, where)
		}
	}
	if strings.Count(where, "ILIKE") != 5 {
		t.Fatalf("search matches exactly five columns:\n%s", where)
	}
	if strings.Contains(where, "JOIN") {
		t.Fatalf("search must not add joins to the base set:\n%s", where)
	}
	if strings.Contains(where, "e.name") && !strings.Contains(where, "se.name") {
		t.Fatalf("search must not reference the display alias e:\n%s", where)
	}

	// The predicate is one parenthesized OR-group, so it AND-s cleanly with
	// the other filters.
	where, args = taskFilterWhere(unscoped, domain.TaskFilters{Status: ptr(domain.StatusOpen), Search: ptr("vat")})
	if !strings.HasPrefix(where, "\nWHERE "+taskOpen+"\n  AND (ti.name ILIKE $1") || !strings.HasSuffix(where, "))") {
		t.Fatalf("search must be a parenthesized group after the other predicates:\n%s", where)
	}
	if args[0] != "%vat%" {
		t.Fatalf("pattern = %q", args[0])
	}
}

// A blank (or whitespace-only) search is no search: no predicate, no bind.
func TestTaskFilterWhere_BlankSearchIsNoFilter(t *testing.T) {
	for _, term := range []string{"", "   ", "\t\n"} {
		where, args := taskFilterWhere(unscoped, domain.TaskFilters{Search: ptr(term)})
		if where != "" || len(args) != 0 {
			t.Fatalf("%q: got %q with %v", term, where, args)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"100%":           `100\%`,
		"Under_score":    `Under\_score`,
		`O'Brien \ P`:    `O'Brien \\ P`,
		`\%_`:            `\\\%\_`,
		"Müller & Söhne": "Müller & Söhne",
	}
	for in, want := range cases {
		if got := baserepo.EscapeLike(in); got != want {
			t.Fatalf("baserepo.EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every filter together: the binds are numbered in order of appearance and
// each predicate is present exactly once.
func TestTaskFilterWhere_AllFiltersTogether(t *testing.T) {
	where, args := taskFilterWhere(unscoped, domain.TaskFilters{
		WorkflowID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		EntityID:         ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		AssigneeID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430ca"),
		ObligationTypeID: ptr("6ba7b810-9dad-11d1-80b4-00c04fd430cb"),
		TaxType:          ptr("VAT"),
		FinancialYears:   []string{"2025", domain.FinancialYearNone},
		PeriodCode:       ptr("M1"),
		Status:           ptr("in_progress"),
		WorkflowCategory: ptr("recurring"),
		Due:              ptr(domain.DueOverdue),
		DueFrom:          date(t, "2025-01-01"),
		DueTo:            date(t, "2025-12-31"),
		Search:           ptr("acme"),
	})
	for i, want := range []string{
		"ti.workflow_id = $1::uuid",
		"w.entity_id = $2::uuid",
		"ti.assignee_id = $3::uuid",
		"w.obligation_type_id = $4::uuid",
		"WHERE sot.template = $5::varchar",
		"(w.financial_year IS NULL OR w.financial_year = $6::varchar)",
		"ti.period_code = $7::varchar",
		"ti.status = $8::varchar",
		"w.workflow_category = $9::varchar",
		"(" + dueOverdue + ")",
		"ti.due_date >= $10::date",
		"ti.due_date <= $11::date",
		"ti.name ILIKE $12::text",
	} {
		if strings.Count(where, want) != 1 {
			t.Fatalf("predicate %d %q must appear exactly once:\n%s", i, want, where)
		}
	}
	if len(args) != 12 || args[11] != "%acme%" || args[9] != "2025-01-01" || args[10] != "2025-12-31" {
		t.Fatalf("binds = %v", args)
	}
	if strings.Count(where, "\n  AND ") != 12 {
		t.Fatalf("13 predicates join with 12 ANDs:\n%s", where)
	}
}

// Every sort key yields an ORDER BY whose columns all run in the primary
// direction and that ends in the unique instance id — so offset pages are
// stable and one btree can serve the default order either way.
func TestOrderBy_EndsInTheInstanceIdInThePrimaryDirection(t *testing.T) {
	cases := map[string]string{
		domain.SortByDueDate:   "ti.due_date",
		domain.SortByCreatedAt: "ti.created_at",
		domain.SortByStatus:    statusRank,
		domain.SortByWorkflow:  "w.name",
		domain.SortByEntity:    "e.name",
		domain.SortByName:      "ti.name",
		"bogus":                "ti.due_date", // unknown keys fall back to the feed's natural order
	}
	for key, primary := range cases {
		for _, desc := range []bool{false, true} {
			dir := "ASC"
			if desc {
				dir = "DESC"
			}
			other := "DESC"
			if desc {
				other = "ASC"
			}
			got := orderBy(key, desc)
			if !strings.HasPrefix(got, " ORDER BY "+primary+" "+dir) {
				t.Fatalf("%s %s: primary must lead: %q", key, dir, got)
			}
			if !strings.HasSuffix(got, ", ti.id "+dir) {
				t.Fatalf("%s %s: must end in the unique id in the primary direction: %q", key, dir, got)
			}
			if strings.Contains(got, " "+other) {
				t.Fatalf("%s %s: every column must run in the primary direction: %q", key, dir, got)
			}
			if !strings.Contains(got, "ti.order_index "+dir) {
				t.Fatalf("%s %s: order_index is part of the tie-break: %q", key, dir, got)
			}
			if key != domain.SortByDueDate && key != domain.SortByCreatedAt && key != "bogus" &&
				!strings.Contains(got, ", ti.due_date "+dir+", ti.order_index "+dir+", ti.id "+dir) {
				t.Fatalf("%s %s: the feed's natural order breaks ties: %q", key, dir, got)
			}
		}
	}
	// Instances without an entity sort last whichever way the entity sort runs.
	for _, desc := range []bool{false, true} {
		if got := orderBy(domain.SortByEntity, desc); !strings.Contains(got, "e.name ASC NULLS LAST") && !strings.Contains(got, "e.name DESC NULLS LAST") {
			t.Fatalf("entity sort must put NULL entities last: %q", got)
		}
	}
}

// The workflow-stats filters follow the same rule: predicates only for what
// is set, on the workflow row, numbered in order; `none` is IS NULL.
func TestWorkflowStatsWhere_OnlySetFiltersBecomePredicates(t *testing.T) {
	where, args := workflowStatsWhere(unscoped, domain.WorkflowStatsFilters{})
	if where != "" || len(args) != 0 {
		t.Fatalf("no filter must render no WHERE, got %q with %v", where, args)
	}

	where, args = workflowStatsWhere(unscoped, domain.WorkflowStatsFilters{
		WorkflowID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		EntityID:         ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		FinancialYears:   []string{"2025", "2026"},
		Status:           ptr("active"),
		WorkflowCategory: ptr("recurring"),
	})
	want := "\nWHERE w.id = $1::uuid" +
		"\n  AND w.entity_id = $2::uuid" +
		"\n  AND w.financial_year = ANY($3::varchar[])" +
		"\n  AND w.status = $4::varchar" +
		"\n  AND w.workflow_category = $5::varchar"
	if where != want {
		t.Fatalf("got %q, want %q", where, want)
	}
	if len(args) != 5 || args[3] != "active" {
		t.Fatalf("binds = %v", args)
	}

	where, args = workflowStatsWhere(unscoped, domain.WorkflowStatsFilters{FinancialYears: []string{domain.FinancialYearNone}})
	if where != "\nWHERE w.financial_year IS NULL" || len(args) != 0 {
		t.Fatalf("got %q with %v", where, args)
	}
	where, args = workflowStatsWhere(unscoped, domain.WorkflowStatsFilters{Status: ptr("draft")})
	if where != "\nWHERE w.status = $1::varchar" || len(args) != 1 {
		t.Fatalf("got %q with %v", where, args)
	}
	if strings.Contains(where, "IS NULL OR $") {
		t.Fatalf("a NULL-tolerant predicate must never be emitted:\n%s", where)
	}
}

// The stats statement is FROM … [WHERE …] GROUP BY, so the dynamic clause
// slots between the LEFT JOIN and the GROUP BY.
func TestWorkflowStatsSQL_ShapeAdmitsTheWhereClause(t *testing.T) {
	if !strings.HasSuffix(workflowStatsSelect, "LEFT JOIN task_instances ti ON ti.workflow_id = w.id") {
		t.Fatalf("the select must end at the join so the WHERE can follow:\n%s", workflowStatsSelect)
	}
	if !strings.HasPrefix(workflowStatsGroup, "\nGROUP BY w.id") || !strings.HasSuffix(workflowStatsGroup, "ORDER BY w.id") {
		t.Fatalf("the group clause must open with GROUP BY and end in the unique workflow id:\n%s", workflowStatsGroup)
	}
}
