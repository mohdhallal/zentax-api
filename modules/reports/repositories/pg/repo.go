// Package pg is the Postgres read model behind /reports. Hand-written SQL with
// LEFT JOINs over RLS-scoped tables only: every query runs on the request
// transaction that carries the app.tenant_id GUC (ADR-0004), so tenant
// isolation holds without a single tenant_id predicate here. users is not
// RLS'd, but it is only ever reached through an RLS-scoped foreign key.
package pg

import (
	"context"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.Reader = (*ReportsRepo)(nil)

type ReportsRepo struct {
	db database.ExecerPg
}

func NewReportsRepo(db database.ExecerPg) *ReportsRepo {
	return &ReportsRepo{db: db}
}

// readScope resolves the caller's readable entity set for the report reads
// (ADR-0012 B-3). Every statement in this package hangs off `workflows w`, so
// one predicate on w.entity_id narrows all of them — the heatmap, the
// compliance rows and their summary, the tax-financial figures and their
// GROUPING SETS roll-ups, the task feed and its tile counts, the workflow
// stats and all three raw-export datasets. This is where the whole-group
// export came from: nothing here consulted authorization at all.
//
// Capability: the reports are task reads (`task:read`), which is what every
// route in the module declares except workflow-stats (`workflow:read`). Both
// are held by every role, so the resolved scope is the same either way; the
// capability is passed through so a future divergence in the matrix narrows
// correctly instead of silently.
func (r *ReportsRepo) readScope(ctx context.Context, cap authz.Capability) (authz.ReadScope, error) {
	return authzpg.ReadScope(ctx, r.db, cap)
}

// taskInstanceBase is the instance set every task read is cut from: the
// instances joined to their workflow (inner — an instance always belongs to
// one). Counts and the summary aggregate over it directly; nothing in the
// filters needs the display joins (the search over entity / obligation-type
// names is a semi-join, see taskSearchPredicate).
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

// whereBuilder collects the predicates of the filters that are SET — and only
// those — numbering their binds $1..$n in order of appearance. Assembling the
// predicates dynamically keeps every statement shape specific to its request:
// pgx's statement cache falls back to a generic plan after a few executions
// of a `($n IS NULL OR col = $n)` statement, which loses the index on the
// column. Column names are code-owned constants; caller data only ever
// travels as a bind parameter.
type whereBuilder struct {
	preds []string
	args  []any
}

// bind appends a parameter and returns its placeholder.
func (b *whereBuilder) bind(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *whereBuilder) add(pred string) {
	b.preds = append(b.preds, pred)
}

// where renders the clause (empty when nothing is set) with its arguments.
func (b *whereBuilder) where() (string, []any) {
	if len(b.preds) == 0 {
		return "", b.args
	}
	return "\nWHERE " + strings.Join(b.preds, "\n  AND "), b.args
}

// taskFilterWhere renders the WHERE clause for the caller's read scope and the
// task filters that are set. The scope goes first so its bind is $1: it is the
// one predicate that is not optional.
func taskFilterWhere(scope authz.ReadScope, f domain.TaskFilters) (string, []any) {
	var b whereBuilder
	if pred := scope.EntityPredicate("w.entity_id", b.bind); pred != "" {
		b.add(pred)
	}
	if f.WorkflowID != nil {
		b.add("ti.workflow_id = " + b.bind(*f.WorkflowID) + "::uuid")
	}
	if f.EntityID != nil {
		b.add("w.entity_id = " + b.bind(*f.EntityID) + "::uuid")
	}
	if f.AssigneeID != nil {
		if *f.AssigneeID == domain.AssigneeUnassigned {
			b.add("ti.assignee_id IS NULL")
		} else {
			b.add("ti.assignee_id = " + b.bind(*f.AssigneeID) + "::uuid")
		}
	}
	if f.ObligationTypeID != nil {
		b.add("w.obligation_type_id = " + b.bind(*f.ObligationTypeID) + "::uuid")
	}
	if f.TaxType != nil {
		// Semi-join on the (RLS-scoped) obligation types of that template: the
		// base set stays free of display joins.
		b.add("w.obligation_type_id IN (SELECT sot.id FROM obligation_types sot WHERE sot.template = " +
			b.bind(*f.TaxType) + "::varchar)")
	}
	if p := financialYearPredicate(f.FinancialYears, b.bind); p != "" {
		b.add(p)
	}
	if f.PeriodCode != nil {
		b.add("ti.period_code = " + b.bind(*f.PeriodCode) + "::varchar")
	}
	if f.Status != nil {
		if *f.Status == domain.StatusOpen {
			b.add(taskOpen)
		} else {
			b.add("ti.status = " + b.bind(*f.Status) + "::varchar")
		}
	}
	if f.WorkflowCategory != nil {
		b.add("w.workflow_category = " + b.bind(*f.WorkflowCategory) + "::varchar")
	}
	if f.Due != nil {
		// The DTO's enum guards the value; the window is the summary's own
		// predicate, so a tile and its drill-down agree by construction.
		switch *f.Due {
		case domain.DueOverdue:
			b.add("(" + dueOverdue + ")")
		case domain.DueToday:
			b.add("(" + dueToday + ")")
		case domain.DueThisWeek:
			b.add("(" + dueThisWeek + ")")
		}
	}
	if f.DueFrom != nil {
		b.add("ti.due_date >= " + b.bind(f.DueFrom.String()) + "::date")
	}
	if f.DueTo != nil {
		b.add("ti.due_date <= " + b.bind(f.DueTo.String()) + "::date")
	}
	if pattern := searchPattern(f.Search); pattern != "" {
		b.add(taskSearchPredicate(b.bind(pattern)))
	}
	return b.where()
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

// searchPattern turns the raw search term into the ILIKE pattern: trimmed,
// LIKE metacharacters escaped so they match literally, wrapped in %…%. Empty
// (or whitespace-only) means no search.
func searchPattern(s *string) string {
	if s == nil {
		return ""
	}
	term := strings.TrimSpace(*s)
	if term == "" {
		return ""
	}
	return "%" + baserepo.EscapeLike(term) + "%"
}

// taskSearchPredicate matches the pattern bound at placeholder p (built by
// searchPattern, '\' as the escape) against the instance's own text (name,
// period code), its workflow's name and — through semi-joins, so the COUNT
// and the summary never need the display LEFT JOINs — the names of its entity
// and obligation type.
func taskSearchPredicate(p string) string {
	like := " ILIKE " + p + "::text ESCAPE '\\'"
	return "(ti.name" + like +
		"\n       OR ti.period_code" + like +
		"\n       OR w.name" + like +
		"\n       OR EXISTS (SELECT 1 FROM entities se WHERE se.id = w.entity_id AND se.name" + like + ")" +
		"\n       OR EXISTS (SELECT 1 FROM obligation_types sot WHERE sot.id = w.obligation_type_id AND sot.name" + like + "))"
}

// orderBy maps the validated sort key to a deterministic ORDER BY: the primary
// expression, then the tie-breakers — the feed's natural order (due_date,
// order_index) where the primary is something else — and finally the unique
// instance id, ALL in the primary direction, so offset pages are stable where
// ties are densest (one instance per template per period shares a due date)
// and one btree ((tenant_id, due_date, order_index, id), migration 22) can
// serve the default order forwards or backwards.
func orderBy(column string, desc bool) string {
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	feed := ", ti.due_date " + dir + ", ti.order_index " + dir + ", ti.id " + dir
	switch column {
	case domain.SortByCreatedAt:
		return " ORDER BY ti.created_at " + dir + ", ti.order_index " + dir + ", ti.id " + dir
	case domain.SortByStatus:
		return " ORDER BY " + statusRank + " " + dir + feed
	case domain.SortByWorkflow:
		return " ORDER BY w.name " + dir + feed
	case domain.SortByEntity:
		// Instances of project workflows have no entity: last either way.
		return " ORDER BY e.name " + dir + " NULLS LAST" + feed
	case domain.SortByName:
		return " ORDER BY ti.name " + dir + feed
	default:
		return " ORDER BY ti.due_date " + dir + ", ti.order_index " + dir + ", ti.id " + dir
	}
}

func (r *ReportsRepo) ListTaskInstances(
	ctx context.Context, args domain.ListTaskInstancesArgs,
) ([]domain.TaskInstanceRow, int, error) {
	scope, err := r.readScope(ctx, authz.TaskRead)
	if err != nil {
		return nil, 0, err
	}
	where, params := taskFilterWhere(scope, args.TaskFilters)

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
	scope, err := r.readScope(ctx, authz.TaskRead)
	if err != nil {
		return nil, err
	}
	where, params := taskFilterWhere(scope, filters)
	var summary domain.TaskSummary
	if err := r.db.GetContext(ctx, &summary, taskSummarySelect+where, params...); err != nil {
		return nil, err
	}
	return &summary, nil
}

// workflowStatsSelect: one row per workflow (LEFT JOIN keeps instance-less
// workflows with zero counts); next_due_date is the earliest due date among
// instances that are not yet completed, NULL when there is none. The filters
// (all on the workflow row) go between the FROM and the GROUP BY.
const workflowStatsSelect = `
SELECT w.id AS workflow_id,
       COUNT(ti.id)::int AS total_tasks,
       COUNT(ti.id) FILTER (WHERE ti.status = 'completed')::int AS completed_tasks,
       MIN(ti.due_date) FILTER (WHERE ti.status <> 'completed') AS next_due_date
FROM workflows w
LEFT JOIN task_instances ti ON ti.workflow_id = w.id`

const workflowStatsGroup = `
GROUP BY w.id
ORDER BY w.id`

// workflowStatsWhere renders the WHERE clause for the caller's read scope and
// the workflow-stats filters that are set (same dynamic assembly as the task
// filters).
func workflowStatsWhere(scope authz.ReadScope, f domain.WorkflowStatsFilters) (string, []any) {
	var b whereBuilder
	if pred := scope.EntityPredicate("w.entity_id", b.bind); pred != "" {
		b.add(pred)
	}
	if f.WorkflowID != nil {
		b.add("w.id = " + b.bind(*f.WorkflowID) + "::uuid")
	}
	if f.EntityID != nil {
		b.add("w.entity_id = " + b.bind(*f.EntityID) + "::uuid")
	}
	if p := financialYearPredicate(f.FinancialYears, b.bind); p != "" {
		b.add(p)
	}
	if f.Status != nil {
		b.add("w.status = " + b.bind(*f.Status) + "::varchar")
	}
	if f.WorkflowCategory != nil {
		b.add("w.workflow_category = " + b.bind(*f.WorkflowCategory) + "::varchar")
	}
	return b.where()
}

func (r *ReportsRepo) WorkflowStats(
	ctx context.Context, filters domain.WorkflowStatsFilters,
) ([]domain.WorkflowStats, error) {
	// workflow-stats is the one report route gated by workflow:read.
	scope, err := r.readScope(ctx, authz.WorkflowRead)
	if err != nil {
		return nil, err
	}
	where, params := workflowStatsWhere(scope, filters)
	stats := []domain.WorkflowStats{}
	if err := r.db.SelectContext(ctx, &stats, workflowStatsSelect+where+workflowStatsGroup, params...); err != nil {
		return nil, err
	}
	return stats, nil
}
