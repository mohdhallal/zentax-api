package pg

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Shared SQL building blocks for the compliance / financial reports
// (ADR-0021). Everything here is a constant expression or a function over
// fixed, code-owned identifiers — no caller-controlled text is ever
// interpolated; filters travel as bind parameters.

// tenantZone is the IANA zone stored on the tenant registry row of the
// request's tenant (the `app.tenant_id` GUC the Tx seam binds — the same value
// RLS isolates on). No caller data is interpolated; the zone comes from the
// registry, where PUT /tenant validated it (ADR-0003 / ADR-0023 §6).
const tenantZone = `(SELECT t.timezone FROM tenants t WHERE t.id = current_setting('app.tenant_id', true)::uuid)`

// tenantToday is "today" as the tenant's calendar date. A missing registry row
// yields NULL, which classifies as not_due (fail closed, never "missed").
const tenantToday = `(NOW() AT TIME ZONE ` + tenantZone + `)::date`

// tenantCompletedDate is the calendar date, in the tenant's zone, on which the
// instance was completed — the same notion of "day" the missed rule uses.
const tenantCompletedDate = `(ti.completed_at AT TIME ZONE ` + tenantZone + `)::date`

// tenantWeekEnd is the SATURDAY that ends the tenant's current week (weeks run
// Sunday–Saturday, DOW 0–6): the "this week" boundary of the dashboard and the
// tasks page, matching the UI's task-metrics.ts. Every occurrence of the
// tenantToday sub-select is uncorrelated, so the planner evaluates it once per
// statement (an InitPlan), never per row.
const tenantWeekEnd = `(` + tenantToday + ` + (6 - EXTRACT(DOW FROM ` + tenantToday + `))::int)`

// taskOpen is "open work": every instance that is not completed. The three
// due-window predicates below are guarded by it — a completed instance is
// never overdue, due today or due this week, whatever its due date.
const taskOpen = `ti.status <> 'completed'`

// dueOverdue / dueToday / dueThisWeek are the dashboard's due-window tiles,
// evaluated against the tenant's civil day (ADR-0023 §6) — one definition for
// the summary and, later, for the feed's `due=` filter.
const (
	dueOverdue  = taskOpen + ` AND ti.due_date < ` + tenantToday
	dueToday    = taskOpen + ` AND ti.due_date = ` + tenantToday
	dueThisWeek = taskOpen + ` AND ti.due_date > ` + tenantToday + ` AND ti.due_date <= ` + tenantWeekEnd
)

// statusRank orders instances the way the task board reads: open work by
// stage, then completed, then blocked. Unknown values sort last.
const statusRank = `CASE ti.status
    WHEN 'not_started' THEN 0
    WHEN 'in_progress' THEN 1
    WHEN 'in_review' THEN 2
    WHEN 'pending_approval' THEN 3
    WHEN 'completed' THEN 4
    WHEN 'blocked' THEN 5
    ELSE 6
END`

// complianceClassTemplate classifies task instance `ti` against a DATE column
// (%[1]s: ti.filing_deadline or ti.due_date) — ONE definition shared by the
// heatmap and the compliance-status report (ADR-0021 rule 5):
//
//	completed with a completion instant → on_time | late (completed_at's
//	calendar date IN THE TENANT'S ZONE (%[3]s) vs the deadline, ADR-0002/0003);
//	otherwise missed when the deadline has passed in the tenant's day
//	(%[2]s = tenantToday), else not_due. One definition of "day" throughout.
//
// The heatmap reads "overdue" as missed and "completed late" as late.
const complianceClassTemplate = `CASE
    WHEN ti.status = 'completed' AND ti.completed_at IS NOT NULL THEN
        CASE WHEN %[3]s <= %[1]s THEN 'on_time' ELSE 'late' END
    WHEN %[1]s < %[2]s THEN 'missed'
    ELSE 'not_due'
END`

func complianceClass(deadlineColumn string) string {
	return fmt.Sprintf(complianceClassTemplate, deadlineColumn, tenantToday, tenantCompletedDate)
}

// participatingWorkflows is the rule for the three compliance / financial
// reports: only active or completed RECURRING workflows take part. Project
// workflows generate instances too (one "PROJECT" period), but they have no
// obligation period to be compliant against — they stay visible in the
// task-instance list, workflow-stats and the raw export.
const participatingWorkflows = `w.status IN ('active', 'completed') AND w.workflow_category = 'recurring'`

// reportFiltersWhere binds the shared filter trio as $1..$3 (NULL = any).
const reportFiltersWhere = `
  AND ($1::varchar IS NULL OR w.financial_year = $1::varchar)
  AND ($2::uuid IS NULL OR w.entity_id = $2::uuid)
  AND ($3::uuid IS NULL OR w.obligation_type_id = $3::uuid)`

func filterArgs(f domain.ReportFilters) []any {
	return []any{f.FinancialYear, f.EntityID, f.ObligationTypeID}
}

// reportScopeWhere renders the RBAC read-scope predicate (ADR-0012 B-3) as an
// extra AND on a report's WHERE, plus the argument it binds. Every statement
// in this package keeps its own fixed placeholders, so the scope's bind is
// APPENDED: firstParam is the statement's next free placeholder number and the
// caller appends the returned args last. An unbounded caller (any tenant-wide
// grant) gets "" and no argument — the statement is byte-for-byte the one that
// ran before this increment, which is what keeps the common path free.
//
// The ids are bound as one uuid[] rather than expanded inline as a recursive
// walk per statement, and that is not a stylistic choice: measured on the
// largest tenant, the bound array plans the task page at 0.30 ms against
// 7.3 ms for the inline walk, and its exact count at 24.9 ms against 59.7 ms
// (and 26.7 ms unscoped). See platform/authz/readscope.go.
func reportScopeWhere(scope authz.ReadScope, firstParam int) (string, []any) {
	var params []any
	bind := func(v any) string {
		params = append(params, v)
		return "$" + strconv.Itoa(firstParam+len(params)-1)
	}
	pred := scope.EntityPredicate("w.entity_id", bind)
	if pred == "" {
		return "", nil
	}
	return "\n  AND " + pred, params
}

// numericRegex accepts a plain decimal number ("1000", "-12.5"); anything else
// (text, blank, scientific notation) counts as 0 — the legacy parseFloat →
// NaN → 0 rule, without a cast that could abort the statement.
const numericRegex = `'^-?[0-9]+(\.[0-9]+)?$'`

// jsonNum extracts one tax_data key as a numeric, 0 when absent or not a
// number (ADR-0021 rule 6: fixed key, ->> only, safe cast).
func jsonNum(key string) string {
	v := "ti.tax_data->>'" + key + "'"
	return "CASE WHEN (" + v + ") ~ " + numericRegex + " THEN (" + v + ")::numeric ELSE 0 END"
}

// firstNonZero mirrors the legacy `num(a) || num(b) || num(c)` alias chain:
// the first key whose numeric value is non-zero wins, else 0. Keys come from
// shared/taxkeys (canonical key first).
func firstNonZero(keys []string) string {
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, "NULLIF("+jsonNum(k)+", 0)")
	}
	parts = append(parts, "0")
	return "COALESCE(" + strings.Join(parts, ", ") + ")"
}

// jsonText extracts one tax_data key as text, NULL when absent or blank.
func jsonText(key string) string {
	return "NULLIF(ti.tax_data->>'" + key + "', '')"
}

// jsonTruthy extracts one tax_data key as text, NULL when it is absent or
// JS-falsy (0, false, "") — the legacy `data.key || …` presence test, so a
// numeric template field left at 0 does not render as "Penalty: 0".
func jsonTruthy(key string) string {
	v := "ti.tax_data->>'" + key + "'"
	return "CASE jsonb_typeof(ti.tax_data->'" + key + "')" +
		" WHEN 'number' THEN NULLIF(NULLIF(" + v + ", '0'), '0.0')" +
		" WHEN 'boolean' THEN NULLIF(" + v + ", 'false')" +
		" WHEN 'string' THEN NULLIF(" + v + ", '')" +
		" ELSE NULL END"
}

// firstTruthy is the alias-chain form of jsonTruthy: the first key with a
// JS-truthy value, as text, else NULL.
func firstTruthy(keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, jsonTruthy(k))
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")"
}
