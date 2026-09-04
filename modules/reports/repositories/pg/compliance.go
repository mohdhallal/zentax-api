package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

// complianceFrom is the FROM/WHERE shared by the heatmap and the
// compliance-status report: instances of participating workflows, filtered by
// the shared trio ($1..$3). Entity / obligation type are LEFT (a project
// workflow may have neither).
const complianceFrom = `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
WHERE ` + participatingWorkflows + reportFiltersWhere

// heatmapSQL: one GROUP BY over the filtered instances. $4 is the view mode:
// columns are period codes ('period') or obligation types ('tax-type'). The
// classification against due_date is the shared expression (ADR-0021 rule 5).
var heatmapSQL = `
SELECT COALESCE(e.id::text, 'unknown') AS row_id,
       COALESCE(e.name, 'Unknown Entity') AS row_label,
       CASE WHEN $4::text = '` + domain.ViewModeTaxType + `'
            THEN COALESCE(ot.id::text, 'unknown') ELSE ti.period_code END AS col_id,
       CASE WHEN $4::text = '` + domain.ViewModeTaxType + `'
            THEN COALESCE(ot.name || ' (' || ot.code || ')', 'Unknown') ELSE ti.period_code END AS col_label,
       COUNT(*)::int AS total_tasks,
       COUNT(*) FILTER (WHERE ti.status = 'completed')::int AS completed_tasks,
       COUNT(*) FILTER (WHERE (` + complianceClass("ti.due_date") + `) = 'missed')::int AS overdue_tasks,
       COUNT(*) FILTER (WHERE (` + complianceClass("ti.due_date") + `) = 'late')::int AS completed_late,
       COUNT(*) FILTER (WHERE ti.status IN ('in_progress', 'in_review', 'pending_approval'))::int AS in_progress_tasks,
       string_agg(DISTINCT ti.workflow_id::text, ',') AS workflow_ids,
       MIN(ti.period_end_date) AS first_period_end` +
	complianceFrom + `
GROUP BY 1, 2, 3, 4
ORDER BY row_label, first_period_end, col_id, row_id` // calendar order, not "M10" before "M2"

func (r *ReportsRepo) ComplianceHeatmap(ctx context.Context, args domain.HeatmapArgs) ([]domain.HeatmapCell, error) {
	cells := []domain.HeatmapCell{}
	params := append(filterArgs(args.ReportFilters), args.ViewMode)
	if err := r.db.SelectContext(ctx, &cells, heatmapSQL, params...); err != nil {
		return nil, err
	}
	return cells, nil
}

// complianceClassified is the CTE both compliance-status statements share:
// every participating instance with its classification against the filing
// deadline and its penalty/interest text. penalty_interest follows the legacy
// precedence: tax_data penalty/interest figures (fixed keys) override a
// payment task's notes; otherwise an empty string. $4 is the status filter (NULL = any),
// applied on the computed classification.
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
               WHEN COALESCE(` + jsonTruthy("penaltyAmount") + `, ` + jsonTruthy("penalty") + `,
                             ` + jsonTruthy("interestAmount") + `, ` + jsonTruthy("interest") + `) IS NOT NULL THEN
                   concat_ws(', ',
                       'Penalty: ' || COALESCE(` + jsonTruthy("penaltyAmount") + `, ` + jsonTruthy("penalty") + `),
                       'Interest: ' || COALESCE(` + jsonTruthy("interestAmount") + `, ` + jsonTruthy("interest") + `))
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

// The summary counts the SAME filtered set (legacy computes it after the
// status filter), so total doubles as the page's exact totalCount.
var complianceSummarySQL = complianceClassified + `
SELECT COUNT(*)::int AS total,
       COUNT(*) FILTER (WHERE compliance_status = 'on_time')::int AS on_time,
       COUNT(*) FILTER (WHERE compliance_status = 'late')::int AS late,
       COUNT(*) FILTER (WHERE compliance_status = 'missed')::int AS missed,
       COUNT(*) FILTER (WHERE compliance_status = 'not_due')::int AS not_due
FROM classified
WHERE ($4::text IS NULL OR compliance_status = $4::text)`

func (r *ReportsRepo) ComplianceStatus(
	ctx context.Context, args domain.ComplianceStatusArgs,
) ([]domain.ComplianceRow, domain.ComplianceSummary, error) {
	rows := []domain.ComplianceRow{}
	var summary domain.ComplianceSummary
	params := append(filterArgs(args.ReportFilters), args.Status)
	if err := r.db.SelectContext(ctx, &rows, complianceRowsSQL,
		append(params, args.Limit, args.Offset)...); err != nil {
		return nil, summary, err
	}
	if err := r.db.GetContext(ctx, &summary, complianceSummarySQL, params...); err != nil {
		return nil, summary, err
	}
	return rows, summary, nil
}
