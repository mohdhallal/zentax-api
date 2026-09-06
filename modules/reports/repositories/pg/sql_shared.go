package pg

import (
	"fmt"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
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
