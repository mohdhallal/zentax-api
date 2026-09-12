package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

// Raw export: every workflow (any status) filtered by entity / obligation type
// / category ($1..$3, NULL = any) and an inclusive date-only window ($4/$5,
// half-open on the stored column): workflows.created_at (UTC calendar date)
// for the workflows dataset, the instance's due_date for tasks / tax-data —
// exactly what the legacy export compared.

// exportWorkflowsFrom: the filters are $1..$5, so a narrowed caller's entity
// set binds at $8 for the page (limit $6, offset $7) and at $6 for the count.
// This statement is where the audited whole-group export came from.
func exportWorkflowsFrom(scopeWhere string) string {
	return `
FROM workflows w
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
WHERE ($1::uuid IS NULL OR w.entity_id = $1::uuid)
  AND ($2::uuid IS NULL OR w.obligation_type_id = $2::uuid)
  AND ($3::varchar IS NULL OR w.workflow_category = $3::varchar)
  AND ($4::date IS NULL OR w.created_at >= ($4::date)::timestamp AT TIME ZONE 'UTC')
  AND ($5::date IS NULL OR w.created_at < ($5::date + 1)::timestamp AT TIME ZONE 'UTC')` + scopeWhere
}

const exportWorkflowsSelect = `
SELECT w.name, w.workflow_category AS category, w.project_type, w.financial_year, w.periodicity,
       e.name AS entity_name, e.country, ot.name AS obligation_name, ot.code AS obligation_code,
       ot.template AS tax_type, w.status, w.start_date, w.end_date, w.tasks_sequential, w.created_at`

func exportWorkflowsSQL(scopeWhere string) string {
	return exportWorkflowsSelect + exportWorkflowsFrom(scopeWhere) + `
ORDER BY w.created_at, w.id
LIMIT $6 OFFSET $7`
}

func exportWorkflowsCount(scopeWhere string) string {
	return `SELECT COUNT(*)::int` + exportWorkflowsFrom(scopeWhere)
}

// exportInstancesFrom is shared by tasks and tax-data (the latter adds the
// tax_data predicate). users is not RLS-scoped: the assignee join is pinned
// to the row's tenant like the enriched task list.
func exportInstancesFrom(scopeWhere string) string {
	return `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
LEFT JOIN users u ON u.id = ti.assignee_id AND u.tenant_id = ti.tenant_id
WHERE ($1::uuid IS NULL OR w.entity_id = $1::uuid)
  AND ($2::uuid IS NULL OR w.obligation_type_id = $2::uuid)
  AND ($3::varchar IS NULL OR w.workflow_category = $3::varchar)
  AND ($4::date IS NULL OR ti.due_date >= $4::date)
  AND ($5::date IS NULL OR ti.due_date < $5::date + 1)` + scopeWhere
}

const exportTasksSelect = `
SELECT ti.name, ti.task_type, ti.status, w.name AS workflow_name, w.workflow_category,
       e.name AS entity_name, e.country, ot.name AS obligation_name, ot.template AS tax_type,
       ti.period_code, w.financial_year, u.name AS assignee_name, ti.due_date, ti.filing_deadline,
       ti.completed_at, ti.approval_required, ti.tax_data_status`

func exportTasksSQL(scopeWhere string) string {
	return exportTasksSelect + exportInstancesFrom(scopeWhere) + `
ORDER BY ti.due_date, ti.order_index, ti.id
LIMIT $6 OFFSET $7`
}

func exportTasksCount(scopeWhere string) string {
	return `SELECT COUNT(*)::int` + exportInstancesFrom(scopeWhere)
}

const exportTaxDataWhere = ` AND ti.tax_data IS NOT NULL`

// Figures are the canonical key then its legacy snake_case twin
// (shared/taxkeys Export* pairs): no tax-type gating, no derivation.
var exportTaxDataSelect = `
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
       (` + firstNonZero(taxkeys.ExportEngagementCost) + `)::float8 AS engagement_cost`

func exportTaxDataSQL(scopeWhere string) string {
	return exportTaxDataSelect + exportInstancesFrom(scopeWhere) + exportTaxDataWhere + `
ORDER BY ti.due_date, ti.order_index, ti.id
LIMIT $6 OFFSET $7`
}

func exportTaxDataCount(scopeWhere string) string {
	return `SELECT COUNT(*)::int` + exportInstancesFrom(scopeWhere) + exportTaxDataWhere
}

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
// (same filters) — ADR-0021 rule 2: capped page, exact total. Both are built
// from the caller's read scope, and the scope's bind lands at a different
// placeholder in each (the page carries limit/offset first), so the two
// statements are assembled separately from the one resolved scope.
func (r *ReportsRepo) page(
	ctx context.Context, dest any, rowsSQL, countSQL func(string) string, args domain.ExportArgs,
) (int, error) {
	scope, err := r.readScope(ctx, authz.TaskRead)
	if err != nil {
		return 0, err
	}
	params := exportArgs(args)

	pageParams := append(append([]any{}, params...), args.Limit, args.Offset)
	pageScope, pageScopeArgs := reportScopeWhere(scope, len(pageParams)+1)
	if err := r.db.SelectContext(ctx, dest, rowsSQL(pageScope), append(pageParams, pageScopeArgs...)...); err != nil {
		return 0, err
	}

	countScope, countScopeArgs := reportScopeWhere(scope, len(params)+1)
	var total int
	if err := r.db.GetContext(ctx, &total, countSQL(countScope),
		append(append([]any{}, params...), countScopeArgs...)...); err != nil {
		return 0, err
	}
	return total, nil
}
