package pg

import (
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

func ptr(s string) *string { return &s }

// The WHERE clause is assembled from the filters that are SET — never a
// generic `($n IS NULL OR col = $n)` shape (which pgx's statement cache turns
// into a generic plan that loses the index after a few executions).
func TestTaskFilterWhere_OnlySetFiltersBecomePredicates(t *testing.T) {
	where, args := taskFilterWhere(domain.TaskFilters{})
	if where != "" || len(args) != 0 {
		t.Fatalf("no filter must render no WHERE, got %q with %v", where, args)
	}

	where, args = taskFilterWhere(domain.TaskFilters{
		WorkflowID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		EntityID:         ptr("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		AssigneeID:       ptr("6ba7b810-9dad-11d1-80b4-00c04fd430ca"),
		FinancialYears:   []string{"2025"},
		Status:           ptr("in_progress"),
		WorkflowCategory: ptr("recurring"),
	})
	for _, want := range []string{
		"WHERE ti.workflow_id = $1::uuid",
		"AND w.entity_id = $2::uuid",
		"AND ti.assignee_id = $3::uuid",
		"AND w.financial_year = $4::varchar",
		"AND ti.status = $5::varchar",
		"AND w.workflow_category = $6::varchar",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("WHERE lacks %q:\n%s", want, where)
		}
	}
	if len(args) != 6 || args[0] != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" || args[5] != "recurring" {
		t.Fatalf("binds must follow the predicates in order, got %v", args)
	}
	if strings.Contains(where, "IS NULL OR $") {
		t.Fatalf("a NULL-tolerant predicate must never be emitted:\n%s", where)
	}

	// A subset binds from $1 again: the numbering follows what is set.
	where, args = taskFilterWhere(domain.TaskFilters{Status: ptr("blocked")})
	if where != "\nWHERE ti.status = $1::varchar" || len(args) != 1 || args[0] != "blocked" {
		t.Fatalf("got %q with %v", where, args)
	}
}

// status=open is "not completed" — a literal predicate, no bind.
func TestTaskFilterWhere_OpenIsNotCompleted(t *testing.T) {
	where, args := taskFilterWhere(domain.TaskFilters{Status: ptr(domain.StatusOpen)})
	if where != "\nWHERE "+taskOpen || len(args) != 0 {
		t.Fatalf("got %q with %v", where, args)
	}
	if taskOpen != "ti.status <> 'completed'" {
		t.Fatalf("open work is every status but completed, got %q", taskOpen)
	}
}

// financialYear is a set: one value is an equality, several an ANY(array),
// `none` alone is IS NULL, and `none` with values OR-s the two.
func TestTaskFilterWhere_FinancialYearSet(t *testing.T) {
	cases := []struct {
		years []string
		want  string
		args  int
	}{
		{nil, "", 0},
		{[]string{"2025"}, "\nWHERE w.financial_year = $1::varchar", 1},
		{[]string{"2025", "2026"}, "\nWHERE w.financial_year = ANY($1::varchar[])", 1},
		{[]string{domain.FinancialYearNone}, "\nWHERE w.financial_year IS NULL", 0},
		{[]string{"2025", domain.FinancialYearNone}, "\nWHERE (w.financial_year IS NULL OR w.financial_year = $1::varchar)", 1},
		{[]string{domain.FinancialYearNone, "2025", "2026"},
			"\nWHERE (w.financial_year IS NULL OR w.financial_year = ANY($1::varchar[]))", 1},
	}
	for _, tc := range cases {
		where, args := taskFilterWhere(domain.TaskFilters{FinancialYears: tc.years})
		if where != tc.want {
			t.Fatalf("%v: got %q, want %q", tc.years, where, tc.want)
		}
		if len(args) != tc.args {
			t.Fatalf("%v: got %d binds, want %d", tc.years, len(args), tc.args)
		}
	}
	// The array bind carries the years without the sentinel.
	_, args := taskFilterWhere(domain.TaskFilters{FinancialYears: []string{"2025", domain.FinancialYearNone, "2026"}})
	years, ok := args[0].([]string)
	if !ok || len(years) != 2 || years[0] != "2025" || years[1] != "2026" {
		t.Fatalf("array bind must hold the plain years only, got %v", args)
	}
}

// The count and the summary aggregate over the base set (instances + their
// workflow); only the row projection carries the display LEFT JOINs.
func TestTaskInstanceBase_HasNoDisplayJoins(t *testing.T) {
	if strings.Contains(taskInstanceBase, "LEFT JOIN") {
		t.Fatalf("the base set must not carry LEFT JOINs:\n%s", taskInstanceBase)
	}
	if !strings.HasPrefix(taskInstanceFrom, taskInstanceBase) {
		t.Fatalf("the row projection must extend the base set")
	}
	if !strings.HasSuffix(taskInstanceCount, taskInstanceBase) {
		t.Fatalf("the count must aggregate over the base set:\n%s", taskInstanceCount)
	}
	if !strings.HasSuffix(taskSummarySelect, taskInstanceBase) {
		t.Fatalf("the summary must aggregate over the base set:\n%s", taskSummarySelect)
	}
	for _, want := range []string{
		tenantToday + " AS today",
		"FILTER (WHERE " + dueOverdue + ")::int AS overdue",
		"FILTER (WHERE " + dueToday + ")::int AS due_today",
		"FILTER (WHERE " + dueThisWeek + ")::int AS due_this_week",
		"FILTER (WHERE " + taskOpen + ")::int AS active",
		"FILTER (WHERE ti.status = 'pending_approval')::int AS awaiting_approval",
	} {
		if !strings.Contains(taskSummarySelect, want) {
			t.Fatalf("summary lacks %q:\n%s", want, taskSummarySelect)
		}
	}
	if strings.Contains(strings.ToUpper(taskSummarySelect), "CURRENT_DATE") {
		t.Fatalf("the summary must never read the server's day:\n%s", taskSummarySelect)
	}
}
