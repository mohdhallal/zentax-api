// Package pg is the Postgres read model behind /reports. Hand-written SQL with
// LEFT JOINs over RLS-scoped tables only: every query runs on the request
// transaction that carries the app.tenant_id GUC (ADR-0004), so tenant
// isolation holds without a single tenant_id predicate here. users is not
// RLS'd, but it is only ever reached through an RLS-scoped foreign key.
package pg

import (
	"context"

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

// taskInstanceFrom is the enriched projection's FROM/WHERE. The workflow join
// is inner (an instance always belongs to one); entity, obligation type and
// assignee are LEFT (project workflows have neither; assignee is optional).
// Filters are NULL-tolerant so one statement serves every combination.
const taskInstanceFrom = `
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
-- users is NOT RLS-scoped and assignee_id carries no FK, so the join is pinned
-- to the row's tenant: a foreign uuid resolves to NULL, never to a name.
LEFT JOIN users u ON u.id = ti.assignee_id AND u.tenant_id = ti.tenant_id
WHERE ($1::uuid IS NULL OR ti.workflow_id = $1::uuid)
  AND ($2::uuid IS NULL OR w.entity_id = $2::uuid)
  AND ($3::varchar IS NULL OR ti.status = $3::varchar)`

const taskInstanceSelect = `
SELECT ti.id, ti.workflow_id, ti.workflow_task_id, ti.period_code, ti.name, ti.description,
       ti.task_type, ti.status, ti.assignee_id, u.name AS assignee_name,
       ti.due_date, ti.period_end_date, ti.filing_deadline, ti.approval_required,
       ti.approved_by, ti.approved_at, ti.completed_at, ti.submitted_by, ti.submitted_at,
       ti.rejection_reason, ti.order_index, ti.notes, ti.data_template_id, ti.tax_data_status,
       ti.created_at, ti.updated_at,
       w.name AS workflow_name, w.workflow_category, w.project_type, w.financial_year,
       w.entity_id, e.name AS entity_name,
       w.obligation_type_id, ot.name AS obligation_type_name, ot.template AS tax_type` +
	taskInstanceFrom

const taskInstanceCount = `SELECT COUNT(*)::int` + taskInstanceFrom

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
	rows := []domain.TaskInstanceRow{}
	query := taskInstanceSelect + orderBy(args.SortColumn, args.SortDesc) + " LIMIT $4 OFFSET $5"
	if err := r.db.SelectContext(ctx, &rows, query,
		args.WorkflowID, args.EntityID, args.Status, args.Limit, args.Offset); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total, taskInstanceCount,
		args.WorkflowID, args.EntityID, args.Status); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
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
