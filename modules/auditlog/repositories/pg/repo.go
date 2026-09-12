// Package pg is the Postgres read model for the audit trail. Hand-written SQL
// over RLS-scoped tables only (audit_log, workflow_tasks, task_instances,
// workflows, and — inside the read-scope predicate — entity_obligations and
// documents) plus users, which is reached solely through actor_id of an
// RLS-scoped row — so the tenant boundary holds without a tenant predicate
// (ADR-0004). Strictly SELECT: the table is append-only at the database.
package pg

import (
	"context"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
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

// readScope resolves the caller's readable entity set for the trail
// (ADR-0012 B-3, the same seam every other entity-linked read module uses).
// The capability is the one the route gates — audit:read — so a grant that
// carries audit:read tenant-wide reads the whole trail and pays nothing, while
// an entity-scoped reviewer (the external-advisor shape ADR-0012 exists for)
// resolves a closed entity set.
func (r *AuditLogRepo) readScope(ctx context.Context) (authz.ReadScope, error) {
	return authzpg.ReadScope(ctx, r.db, authz.AuditRead)
}

// auditScopePredicate narrows the trail to the events of resources inside the
// caller's read scope. Empty for an unbounded scope, so a tenant-wide reader's
// statement is byte-identical to what it was before narrowing existed.
//
// An audit row reaches an entity four different ways, one per resource family,
// and all four are needed because the trail is the only read that spans every
// module:
//
//   - workflow / workflow_task / task_instance — through the workflow auditFrom
//     already resolves (rw.id -> w), so w.entity_id is the owning entity;
//   - entity — the audited resource IS the entity, so resource_id is the id;
//   - entity_obligation, document — one join out, as an EXISTS so the row
//     count, the ordering and the count(*) are untouched (documents reach their
//     entity through their workflow, as in the documents module).
//
// Everything else resolves NO entity, and for a NARROWED reader such a row is
// deliberately dropped: obligation types, data templates, the member directory
// and the tenant record are tenant-level, and `readscope.go` states the rule
// for exactly this case — "a row whose owning entity is NULL is NOT in it; a
// tenant-level resource needs a tenant-wide grant, exactly as it does for
// writes". `member.invited` and friends therefore stay fully visible to every
// principal holding a tenant-wide audit:read grant (the predicate is empty for
// them) and are withheld only from a principal scoped to one subtree, who
// cannot read the members' or the catalogue's rows either. Events about a
// resource that has since been DELETED resolve no entity for the same reason
// (the workflow / entity / document row is gone), so they too narrow away —
// fail closed, and still complete for a tenant-wide reader, which is the
// principal a compliance audit uses.
//
// The entity set is bound ONCE and every branch reuses the placeholder: four
// copies of the same uuid[] would be four parameters for the planner to carry.
func auditScopePredicate(scope authz.ReadScope, bind authz.Bind) string {
	if scope.Unbounded() {
		return ""
	}
	if len(scope.EntityIDs()) == 0 {
		return "FALSE"
	}
	in := " = ANY(" + bind(scope.EntityIDs()) + "::uuid[])"
	return "(w.entity_id" + in +
		"\n    OR (a.resource_type = 'entity' AND a.resource_id" + in + ")" +
		"\n    OR (a.resource_type = 'entity_obligation' AND EXISTS (" +
		"SELECT 1 FROM entity_obligations seo WHERE seo.id = a.resource_id AND seo.entity_id" + in + "))" +
		"\n    OR (a.resource_type = 'document' AND EXISTS (" +
		"SELECT 1 FROM documents sd JOIN workflows sw ON sw.id = sd.workflow_id" +
		" WHERE sd.id = a.resource_id AND sw.entity_id" + in + ")))"
}

// auditWhere renders the WHERE clause for the filters that are set, plus the
// caller's read scope. The date bounds are half-open UTC day boundaries —
// [from 00:00Z, to+1 00:00Z) — expressed against the raw column so
// range-partition pruning on occurred_at still applies. Actions is a set: one
// value is an equality, several an ANY. The scope predicate is added LAST, so
// the filters keep binding $1..$n in the order they appear and an unbounded
// scope changes neither the statement nor its parameters.
func auditWhere(args domain.ListArgs, scope authz.ReadScope) (string, []any) {
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
	if pred := auditScopePredicate(scope, b.bind); pred != "" {
		b.add(pred)
	}
	return b.where()
}

// List returns one page of the trail and its exact total. Both statements share
// `where`, so the read scope narrows the TOTAL with the page: a narrowed page
// carrying the tenant's total would report the size of what it withheld.
func (r *AuditLogRepo) List(ctx context.Context, args domain.ListArgs) ([]domain.Entry, int, error) {
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, params := auditWhere(args, scope)

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
