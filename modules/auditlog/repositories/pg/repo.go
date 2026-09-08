// Package pg is the Postgres read model for the audit trail. Hand-written SQL
// over RLS-scoped tables only (audit_log, workflow_tasks, task_instances,
// workflows) plus users, which is reached solely through actor_id of an
// RLS-scoped row — so the tenant boundary holds without a tenant predicate
// (ADR-0004). Strictly SELECT: the table is append-only at the database.
package pg

import (
	"context"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.Reader = (*AuditLogRepo)(nil)

type AuditLogRepo struct {
	db database.ExecerPg
}

func NewAuditLogRepo(db database.ExecerPg) *AuditLogRepo {
	return &AuditLogRepo{db: db}
}

// auditFrom resolves each event's workflow (its own id for workflow events,
// the parent workflow for task-template / task-instance events, NULL for the
// rest). Events whose template / instance has since been deleted resolve to
// NULL (LEFT JOIN). The filters are appended per request by auditWhere.
const auditFrom = `
FROM audit_log a
LEFT JOIN workflow_tasks wt ON a.resource_type = 'workflow_task' AND wt.id = a.resource_id
LEFT JOIN task_instances ti ON a.resource_type = 'task_instance' AND ti.id = a.resource_id
LEFT JOIN LATERAL (
    SELECT CASE a.resource_type
        WHEN 'workflow'      THEN a.resource_id
        WHEN 'workflow_task' THEN wt.workflow_id
        WHEN 'task_instance' THEN ti.workflow_id
    END AS id
) rw ON true
LEFT JOIN workflows w ON w.id = rw.id
-- users is NOT RLS-scoped: pin the actor lookup to the event's tenant.
LEFT JOIN users u ON u.id = a.actor_id AND u.tenant_id = a.tenant_id`

const auditSelect = `
SELECT a.event_id, a.seq, a.action, a.resource_type, a.resource_id, a.actor_id,
       u.name AS actor_name, a.occurred_at, a.request_id, a.details,
       rw.id AS workflow_id, w.name AS workflow_name, a.hash` + auditFrom

// auditOrder: newest first by seq alone. platform/audit.Record assigns seq and
// occurred_at inside the same per-tenant advisory lock (held to commit), so
// the two orders are identical by construction and seq — the chain's own
// order, unique per tenant — is the deterministic key the (tenant_id, seq
// DESC) index serves.
const auditOrder = `
ORDER BY a.seq DESC`

const auditCount = `SELECT COUNT(*)::int` + auditFrom

// whereBuilder collects the predicates of the filters that are SET — and only
// those — numbering their binds $1..$n in order of appearance, so every
// statement shape is specific to its request: a generic `($n IS NULL OR …)`
// shape stops the planner from pruning the occurred_at partitions and using
// the per-tenant indexes once pgx falls back to a generic plan. Column names
// are code-owned constants; caller data only ever travels as a bind.
type whereBuilder struct {
	preds []string
	args  []any
}

func (b *whereBuilder) bind(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *whereBuilder) add(pred string) { b.preds = append(b.preds, pred) }

func (b *whereBuilder) where() (string, []any) {
	if len(b.preds) == 0 {
		return "", b.args
	}
	return "\nWHERE " + strings.Join(b.preds, "\n  AND "), b.args
}

// auditWhere renders the WHERE clause for the filters that are set. The date
// bounds are half-open UTC day boundaries — [from 00:00Z, to+1 00:00Z) —
// expressed against the raw column so range-partition pruning on occurred_at
// still applies. Actions is a set: one value is an equality, several an ANY.
func auditWhere(args domain.ListArgs) (string, []any) {
	var b whereBuilder
	if args.WorkflowID != nil {
		b.add("rw.id = " + b.bind(*args.WorkflowID) + "::uuid")
	}
	if args.ResourceType != nil {
		b.add("a.resource_type = " + b.bind(*args.ResourceType) + "::varchar")
	}
	if args.ResourceID != nil {
		b.add("a.resource_id = " + b.bind(*args.ResourceID) + "::uuid")
	}
	switch len(args.Actions) {
	case 0:
	case 1:
		b.add("a.action = " + b.bind(args.Actions[0]) + "::varchar")
	default:
		b.add("a.action = ANY(" + b.bind(args.Actions) + "::varchar[])")
	}
	if args.From != nil {
		b.add("a.occurred_at >= (" + b.bind(args.From.String()) + "::date)::timestamp AT TIME ZONE 'UTC'")
	}
	if args.To != nil {
		b.add("a.occurred_at < (" + b.bind(args.To.String()) + "::date + 1)::timestamp AT TIME ZONE 'UTC'")
	}
	return b.where()
}

func (r *AuditLogRepo) List(ctx context.Context, args domain.ListArgs) ([]domain.Entry, int, error) {
	where, params := auditWhere(args)

	entries := []domain.Entry{}
	limit := "$" + strconv.Itoa(len(params)+1)
	offset := "$" + strconv.Itoa(len(params)+2)
	query := auditSelect + where + auditOrder + " LIMIT " + limit + " OFFSET " + offset
	pageArgs := append(append([]any{}, params...), args.Limit, args.Offset)
	if err := r.db.SelectContext(ctx, &entries, query, pageArgs...); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total, auditCount+where, params...); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
