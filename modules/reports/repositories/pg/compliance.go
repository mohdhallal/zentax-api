package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

// complianceFrom is the FROM/WHERE shared by the heatmap and the
// compliance-status report: instances of participating (active / completed,
// recurring) workflows, filtered by the shared trio ($1..$3). Entity /
// obligation type are LEFT (a recurring workflow may lack an obligation type).
const complianceFrom = `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
WHERE ` + participatingWorkflows + reportFiltersWhere

// heatmapSQL: one GROUP BY over the filtered instances. $4 is the view mode:
// columns are period codes ('period') or obligation types ('tax-type'). In
// period view the column is the bare period code while a financial year is
// selected ($1) and "<financialYear>:<periodCode>" (label "M1 (FY2019)") when
// none is — ADR-0026 decision 7: M1 of FY2019 and M1 of FY2026 must never
// merge into one column. $5 caps the cells (domain.MaxHeatmapCells + 1: the
// handler turns the extra row into a 400). The classification against
// due_date is the shared expression (ADR-0021 rule 5).
var heatmapSQL = `
SELECT COALESCE(e.id::text, 'unknown') AS row_id,
       COALESCE(e.name, 'Unknown Entity') AS row_label,
       CASE WHEN $4::text = '` + domain.ViewModeTaxType + `' THEN COALESCE(ot.id::text, 'unknown')
            WHEN $1::varchar IS NULL THEN COALESCE(w.financial_year, '') || ':' || ti.period_code
            ELSE ti.period_code END AS col_id,
       CASE WHEN $4::text = '` + domain.ViewModeTaxType + `' THEN COALESCE(ot.name || ' (' || ot.code || ')', 'Unknown')
            WHEN $1::varchar IS NULL THEN ti.period_code || ' (FY' || COALESCE(w.financial_year, '') || ')'
            ELSE ti.period_code END AS col_label,
       COUNT(*)::int AS total_tasks,
       COUNT(*) FILTER (WHERE ti.status = 'completed')::int AS completed_tasks,
       COUNT(*) FILTER (WHERE (` + complianceClass("ti.due_date") + `) = 'missed')::int AS overdue_tasks,
       COUNT(*) FILTER (WHERE (` + complianceClass("ti.due_date") + `) = 'late')::int AS completed_late,
       COUNT(*) FILTER (WHERE ti.status IN ('in_progress', 'in_review', 'pending_approval'))::int AS in_progress_tasks,
       string_agg(DISTINCT ti.workflow_id::text, ',') AS workflow_ids,
       MIN(ti.period_end_date) AS first_period_end` +
	complianceFrom + `
GROUP BY 1, 2, 3, 4
ORDER BY row_label, first_period_end, col_id, row_id
LIMIT $5` // calendar order, not "M10" before "M2"

func (r *ReportsRepo) ComplianceHeatmap(ctx context.Context, args domain.HeatmapArgs) ([]domain.HeatmapCell, error) {
	cells := []domain.HeatmapCell{}
	params := append(filterArgs(args.ReportFilters), args.ViewMode, args.Limit)
	if err := r.db.SelectContext(ctx, &cells, heatmapSQL, params...); err != nil {
		return nil, err
	}
	return cells, nil
}

// complianceClassified is the CTE both compliance-status statements share:
// every participating instance with its classification against the filing
// deadline and its penalty/interest text. penalty_interest follows the legacy
// precedence: tax_data penalty/interest figures (the shared/taxkeys chains)
// override a payment task's notes; otherwise an empty string. $4 is the
// status filter (NULL = any), applied on the computed classification by the
// statements below.
var complianceClassified = `
WITH classified AS (
    SELECT ti.id AS task_instance_id,
           ti.workflow_id,
           ti.period_code AS period,
           ti.filing_deadline,
           ti.completed_at,
           COALESCE(e.name, 'Unknown Entity') AS entity_name,
           COALESCE(e.id::text, '') AS entity_id,
           COALESCE(ot.template, '') AS tax_type,
           COALESCE(ot.name, 'Unknown Obligation') AS obligation_name,
           COALESCE(ot.code, '') AS obligation_code,
           (` + complianceClass("ti.filing_deadline") + `) AS compliance_status,
           CASE
               WHEN COALESCE(` + firstTruthy(taxkeys.PenaltyAliases) + `, ` + firstTruthy(taxkeys.InterestAliases) + `) IS NOT NULL THEN
                   concat_ws(', ',
                       'Penalty: ' || ` + firstTruthy(taxkeys.PenaltyAliases) + `,
                       'Interest: ' || ` + firstTruthy(taxkeys.InterestAliases) + `)
               WHEN ti.task_type = 'payment' AND ti.notes IS NOT NULL THEN ti.notes
               ELSE ''
           END AS penalty_interest` +
	complianceFrom + `
)
`

var complianceRowsSQL = complianceClassified + `
SELECT entity_name, entity_id, tax_type, obligation_name, obligation_code, period,
       filing_deadline, completed_at, compliance_status, penalty_interest,
       workflow_id, task_instance_id
FROM classified
WHERE ($4::text IS NULL OR compliance_status = $4::text)
ORDER BY entity_name, obligation_name, period, task_instance_id
LIMIT $5 OFFSET $6`

// The summary counts the WHOLE classified set (year / entity / obligation
// filters applied, the status filter not) — the cards describe the population
// the page was cut from — while filtered_total is the exact size of the
// status-filtered set the rows page through (totalCount).
var complianceSummarySQL = complianceClassified + `
SELECT COUNT(*)::int AS total,
       COUNT(*) FILTER (WHERE compliance_status = 'on_time')::int AS on_time,
       COUNT(*) FILTER (WHERE compliance_status = 'late')::int AS late,
       COUNT(*) FILTER (WHERE compliance_status = 'missed')::int AS missed,
       COUNT(*) FILTER (WHERE compliance_status = 'not_due')::int AS not_due,
       COUNT(*) FILTER (WHERE $4::text IS NULL OR compliance_status = $4::text)::int AS filtered_total
FROM classified`

type complianceSummaryRow struct {
	domain.ComplianceSummary
	FilteredTotal int `db:"filtered_total"`
}

func (r *ReportsRepo) ComplianceStatus(
	ctx context.Context, args domain.ComplianceStatusArgs,
) (*domain.ComplianceStatusResult, error) {
	res := &domain.ComplianceStatusResult{Rows: []domain.ComplianceRow{}}
	params := append(filterArgs(args.ReportFilters), args.Status)
	if err := r.db.SelectContext(ctx, &res.Rows, complianceRowsSQL,
		append(params, args.Limit, args.Offset)...); err != nil {
		return nil, err
	}
	var summary complianceSummaryRow
	if err := r.db.GetContext(ctx, &summary, complianceSummarySQL, params...); err != nil {
		return nil, err
	}
	res.Summary = summary.ComplianceSummary
	res.TotalCount = summary.FilteredTotal
	return res, nil
}
