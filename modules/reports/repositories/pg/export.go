package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

// Raw export: every workflow (any status) filtered by entity / obligation type
// / category ($1..$3, NULL = any) and an inclusive date-only window ($4/$5,
// half-open on the stored column): workflows.created_at (UTC calendar date)
// for the workflows dataset, the instance's due_date for tasks / tax-data —
// exactly what the legacy export compared.

const exportWorkflowsFrom = `
FROM workflows w
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
WHERE ($1::uuid IS NULL OR w.entity_id = $1::uuid)
  AND ($2::uuid IS NULL OR w.obligation_type_id = $2::uuid)
  AND ($3::varchar IS NULL OR w.workflow_category = $3::varchar)
  AND ($4::date IS NULL OR w.created_at >= ($4::date)::timestamp AT TIME ZONE 'UTC')
  AND ($5::date IS NULL OR w.created_at < ($5::date + 1)::timestamp AT TIME ZONE 'UTC')`

const exportWorkflowsSQL = `
SELECT w.name, w.workflow_category AS category, w.project_type, w.financial_year, w.periodicity,
       e.name AS entity_name, e.country, ot.name AS obligation_name, ot.code AS obligation_code,
       ot.template AS tax_type, w.status, w.start_date, w.end_date, w.tasks_sequential, w.created_at` +
	exportWorkflowsFrom + `
ORDER BY w.created_at, w.id
LIMIT $6 OFFSET $7`

const exportWorkflowsCount = `SELECT COUNT(*)::int` + exportWorkflowsFrom

// exportInstancesFrom is shared by tasks and tax-data (the latter adds the
// tax_data predicate). users is not RLS-scoped: the assignee join is pinned
// to the row's tenant like the enriched task list.
const exportInstancesFrom = `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
LEFT JOIN users u ON u.id = ti.assignee_id AND u.tenant_id = ti.tenant_id
WHERE ($1::uuid IS NULL OR w.entity_id = $1::uuid)
  AND ($2::uuid IS NULL OR w.obligation_type_id = $2::uuid)
  AND ($3::varchar IS NULL OR w.workflow_category = $3::varchar)
  AND ($4::date IS NULL OR ti.due_date >= $4::date)
  AND ($5::date IS NULL OR ti.due_date < $5::date + 1)`

const exportTasksSQL = `
SELECT ti.name, ti.task_type, ti.status, w.name AS workflow_name, w.workflow_category,
       e.name AS entity_name, e.country, ot.name AS obligation_name, ot.template AS tax_type,
       ti.period_code, w.financial_year, u.name AS assignee_name, ti.due_date, ti.filing_deadline,
       ti.completed_at, ti.approval_required, ti.tax_data_status` +
	exportInstancesFrom + `
ORDER BY ti.due_date, ti.order_index, ti.id
LIMIT $6 OFFSET $7`

const exportTasksCount = `SELECT COUNT(*)::int` + exportInstancesFrom

const exportTaxDataWhere = ` AND ti.tax_data IS NOT NULL`

// Figures are the canonical key then its legacy snake_case twin
// (shared/taxkeys Export* pairs): no tax-type gating, no derivation.
var exportTaxDataSQL = `
SELECT ti.name, w.name AS workflow_name, e.name AS entity_name, e.country, ot.name AS obligation_name,
       ot.template AS tax_type, ti.period_code, w.financial_year, ti.tax_data_status,
       (` + firstNonZero(taxkeys.ExportOutputVat) + `)::float8 AS output_vat,
       (` + firstNonZero(taxkeys.ExportInputVat) + `)::float8 AS input_vat,
       (` + firstNonZero(taxkeys.ExportNetVat) + `)::float8 AS net_vat,
       (` + firstNonZero(taxkeys.ExportTaxableIncome) + `)::float8 AS taxable_income,
       (` + firstNonZero(taxkeys.ExportTaxLiability) + `)::float8 AS tax_liability,
       (` + firstNonZero(taxkeys.ExportWhtAmount) + `)::float8 AS wht_amount,
       (` + firstNonZero(taxkeys.ExportPenaltyAmount) + `)::float8 AS penalty_amount,
       (` + firstNonZero(taxkeys.ExportInterestAmount) + `)::float8 AS interest_amount,
       (` + firstNonZero(taxkeys.ExportEngagementCost) + `)::float8 AS engagement_cost` +
	exportInstancesFrom + exportTaxDataWhere + `
ORDER BY ti.due_date, ti.order_index, ti.id
LIMIT $6 OFFSET $7`

const exportTaxDataCount = `SELECT COUNT(*)::int` + exportInstancesFrom + exportTaxDataWhere

func exportArgs(a domain.ExportArgs) []any {
	return []any{a.EntityID, a.ObligationTypeID, a.Category, dateArg(a.DateFrom), dateArg(a.DateTo)}
}

// dateArg binds an optional legal date as YYYY-MM-DD text (NULL when absent).
func dateArg(d *dateonly.Date) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

func (r *ReportsRepo) ExportWorkflows(ctx context.Context, args domain.ExportArgs) ([]domain.ExportWorkflowRow, int, error) {
	rows := []domain.ExportWorkflowRow{}
	total, err := r.page(ctx, &rows, exportWorkflowsSQL, exportWorkflowsCount, args)
	return rows, total, err
}

func (r *ReportsRepo) ExportTasks(ctx context.Context, args domain.ExportArgs) ([]domain.ExportTaskRow, int, error) {
	rows := []domain.ExportTaskRow{}
	total, err := r.page(ctx, &rows, exportTasksSQL, exportTasksCount, args)
	return rows, total, err
}

func (r *ReportsRepo) ExportTaxData(ctx context.Context, args domain.ExportArgs) ([]domain.ExportTaxDataRow, int, error) {
	rows := []domain.ExportTaxDataRow{}
	total, err := r.page(ctx, &rows, exportTaxDataSQL, exportTaxDataCount, args)
	return rows, total, err
}

// page runs the row statement (filters + limit/offset) and the count statement
// (same filters) — ADR-0021 rule 2: capped page, exact total.
func (r *ReportsRepo) page(ctx context.Context, dest any, rowsSQL, countSQL string, args domain.ExportArgs) (int, error) {
	params := exportArgs(args)
	if err := r.db.SelectContext(ctx, dest, rowsSQL, append(params, args.Limit, args.Offset)...); err != nil {
		return 0, err
	}
	var total int
	if err := r.db.GetContext(ctx, &total, countSQL, params...); err != nil {
		return 0, err
	}
	return total, nil
}
