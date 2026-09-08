// Package pg is the Postgres read model behind /reports. Hand-written SQL with
// LEFT JOINs over RLS-scoped tables only: every query runs on the request
// transaction that carries the app.tenant_id GUC (ADR-0004), so tenant
// isolation holds without a single tenant_id predicate here. users is not
// RLS'd, but it is only ever reached through an RLS-scoped foreign key.
package pg

import (
	"context"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.Reader = (*ReportsRepo)(nil)

type ReportsRepo struct {
	db database.ExecerPg
}

func NewReportsRepo(db database.ExecerPg) *ReportsRepo {
	return &ReportsRepo{db: db}
}

// taskInstanceBase is the instance set every task read is cut from: the
// instances joined to their workflow (inner — an instance always belongs to
// one). Counts and the summary aggregate over it directly; nothing in the
// filters needs the display joins.
const taskInstanceBase = `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id`

// taskInstanceFrom is the enriched projection's FROM: the base plus the
// display joins — entity, obligation type and assignee are LEFT (project
// workflows have neither; assignee is optional). None of them changes the
// row count, so the exact total is COUNTed over taskInstanceBase.
const taskInstanceFrom = taskInstanceBase + `
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
-- users is NOT RLS-scoped and assignee_id carries no FK, so the join is pinned
-- to the row's tenant: a foreign uuid resolves to NULL, never to a name.
LEFT JOIN users u ON u.id = ti.assignee_id AND u.tenant_id = ti.tenant_id`

const taskInstanceSelect = `
SELECT ti.id, ti.workflow_id, ti.workflow_task_id, ti.period_code, ti.name, ti.description,
       ti.task_type, ti.status, ti.assignee_id, u.name AS assignee_name,
       ti.due_date, ti.period_end_date, ti.filing_deadline, ti.payment_deadline, ti.approval_required,
       ti.approved_by, ti.approved_at, ti.completed_at, ti.submitted_by, ti.submitted_at,
       ti.rejection_reason, ti.order_index, ti.notes, ti.data_template_id, ti.tax_data_status,
       ti.created_at, ti.updated_at,
       w.name AS workflow_name, w.workflow_category, w.project_type, w.financial_year,
       w.entity_id, e.name AS entity_name,
       w.obligation_type_id, ot.name AS obligation_type_name, ot.template AS tax_type` +
	taskInstanceFrom

const taskInstanceCount = `SELECT COUNT(*)::int` + taskInstanceBase

// taskFilterWhere renders the WHERE clause for the filters that are SET —
// and only those — binding them as $1..$n in order, and returns the clause
// (empty when nothing is set) with its arguments. Assembling the predicates
// dynamically keeps every statement shape specific to its request: pgx's
// statement cache falls back to a generic plan after a few executions of a
// `($n IS NULL OR col = $n)` statement, which loses the index on the column.
// Column names are code-owned constants; caller data only ever travels as a
// bind parameter.
func taskFilterWhere(f domain.TaskFilters) (string, []any) {
	var (
		preds []string
		args  []any
	)
	bind := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.WorkflowID != nil {
		preds = append(preds, "ti.workflow_id = "+bind(*f.WorkflowID)+"::uuid")
	}
	if f.EntityID != nil {
		preds = append(preds, "w.entity_id = "+bind(*f.EntityID)+"::uuid")
	}
	if f.AssigneeID != nil {
		preds = append(preds, "ti.assignee_id = "+bind(*f.AssigneeID)+"::uuid")
	}
	if p := financialYearPredicate(f.FinancialYears, bind); p != "" {
		preds = append(preds, p)
	}
	if f.Status != nil {
		if *f.Status == domain.StatusOpen {
			preds = append(preds, taskOpen)
		} else {
			preds = append(preds, "ti.status = "+bind(*f.Status)+"::varchar")
		}
	}
	if f.WorkflowCategory != nil {
		preds = append(preds, "w.workflow_category = "+bind(*f.WorkflowCategory)+"::varchar")
	}
	if len(preds) == 0 {
		return "", args
	}
	return "\nWHERE " + strings.Join(preds, "\n  AND "), args
}

// financialYearPredicate OR-s the requested years; the FinancialYearNone
// sentinel stands for a workflow with no financial year (project workflows).
func financialYearPredicate(years []string, bind func(any) string) string {
	values := make([]string, 0, len(years))
	none := false
	for _, y := range years {
		if y == domain.FinancialYearNone {
			none = true
			continue
		}
		values = append(values, y)
	}
	switch {
	case len(values) == 0 && !none:
		return ""
	case len(values) == 0:
		return "w.financial_year IS NULL"
	}
	var in string
	if len(values) == 1 {
		in = "w.financial_year = " + bind(values[0]) + "::varchar"
	} else {
		in = "w.financial_year = ANY(" + bind(values) + "::varchar[])"
	}
	if none {
		return "(w.financial_year IS NULL OR " + in + ")"
	}
	return in
}

// orderBy maps the validated sort column to a deterministic ORDER BY. The
// order_index + id tie-breakers keep pages stable when many instances share a
// due date (the common case: one per template per period).
func orderBy(column string, desc bool) string {
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	switch column {
	case domain.SortByCreatedAt:
		return " ORDER BY ti.created_at " + dir + ", ti.order_index ASC, ti.id ASC"
	default:
		return " ORDER BY ti.due_date " + dir + ", ti.order_index ASC, ti.id ASC"
	}
}

func (r *ReportsRepo) ListTaskInstances(
	ctx context.Context, args domain.ListTaskInstancesArgs,
) ([]domain.TaskInstanceRow, int, error) {
	where, params := taskFilterWhere(args.TaskFilters)

	rows := []domain.TaskInstanceRow{}
	limit := "$" + strconv.Itoa(len(params)+1)
	offset := "$" + strconv.Itoa(len(params)+2)
	query := taskInstanceSelect + where + orderBy(args.SortColumn, args.SortDesc) +
		" LIMIT " + limit + " OFFSET " + offset
	pageArgs := append(append([]any{}, params...), args.Limit, args.Offset)
	if err := r.db.SelectContext(ctx, &rows, query, pageArgs...); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total, taskInstanceCount+where, params...); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// taskSummarySelect is the dashboard's tile set as ONE aggregate over the
// filtered base set: a COUNT(*) FILTER per tile plus the tenant's civil today.
// Every tenantToday occurrence is an uncorrelated sub-select the planner
// hoists into an InitPlan evaluated once per statement — never per row. The
// aggregate always yields exactly one row, so an empty set reads as zeros
// with today still set.
const taskSummarySelect = `
SELECT ` + tenantToday + ` AS today,
       COUNT(*)::int AS total,
       COUNT(*) FILTER (WHERE ti.status = 'completed')::int AS completed,
       COUNT(*) FILTER (WHERE ` + taskOpen + `)::int AS active,
       COUNT(*) FILTER (WHERE ` + dueOverdue + `)::int AS overdue,
       COUNT(*) FILTER (WHERE ` + dueToday + `)::int AS due_today,
       COUNT(*) FILTER (WHERE ` + dueThisWeek + `)::int AS due_this_week,
       COUNT(*) FILTER (WHERE ti.status = 'pending_approval')::int AS awaiting_approval,
       COUNT(*) FILTER (WHERE ti.status = 'not_started')::int AS not_started,
       COUNT(*) FILTER (WHERE ti.status = 'in_progress')::int AS in_progress,
       COUNT(*) FILTER (WHERE ti.status = 'in_review')::int AS in_review,
       COUNT(*) FILTER (WHERE ti.status = 'pending_approval')::int AS pending_approval,
       COUNT(*) FILTER (WHERE ti.status = 'blocked')::int AS blocked` +
	taskInstanceBase

func (r *ReportsRepo) TaskSummary(ctx context.Context, filters domain.TaskFilters) (*domain.TaskSummary, error) {
	where, params := taskFilterWhere(filters)
	var summary domain.TaskSummary
	if err := r.db.GetContext(ctx, &summary, taskSummarySelect+where, params...); err != nil {
		return nil, err
	}
	return &summary, nil
}

// workflowStatsSQL: one row per workflow (LEFT JOIN keeps instance-less
// workflows with zero counts); next_due_date is the earliest due date among
// instances that are not yet completed, NULL when there is none.
const workflowStatsSQL = `
SELECT w.id AS workflow_id,
       COUNT(ti.id)::int AS total_tasks,
       COUNT(ti.id) FILTER (WHERE ti.status = 'completed')::int AS completed_tasks,
       MIN(ti.due_date) FILTER (WHERE ti.status <> 'completed') AS next_due_date
FROM workflows w
LEFT JOIN task_instances ti ON ti.workflow_id = w.id
GROUP BY w.id
ORDER BY w.id`

func (r *ReportsRepo) WorkflowStats(ctx context.Context) ([]domain.WorkflowStats, error) {
	stats := []domain.WorkflowStats{}
	if err := r.db.SelectContext(ctx, &stats, workflowStatsSQL); err != nil {
		return nil, err
	}
	return stats, nil
}
