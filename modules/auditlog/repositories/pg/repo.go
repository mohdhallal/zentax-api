// Package pg is the Postgres read model for the audit trail. Hand-written SQL
// over RLS-scoped tables only (audit_log, workflow_tasks, task_instances,
// workflows) plus users, which is reached solely through actor_id of an
// RLS-scoped row — so the tenant boundary holds without a tenant predicate
// (ADR-0004). Strictly SELECT: the table is append-only at the database.
package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
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
// rest) and applies every filter. Date bounds are half-open UTC day
// boundaries — [from 00:00Z, to+1 00:00Z) — expressed against the raw column
// so range-partition pruning on occurred_at still applies. Events whose
// template / instance has since been deleted resolve to NULL (LEFT JOIN).
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
LEFT JOIN users u ON u.id = a.actor_id AND u.tenant_id = a.tenant_id
WHERE ($1::uuid IS NULL OR rw.id = $1::uuid)
  AND ($2::varchar IS NULL OR a.resource_type = $2::varchar)
  AND ($3::uuid IS NULL OR a.resource_id = $3::uuid)
  AND ($4::varchar IS NULL OR a.action = $4::varchar)
  AND ($5::date IS NULL OR a.occurred_at >= ($5::date)::timestamp AT TIME ZONE 'UTC')
  AND ($6::date IS NULL OR a.occurred_at < ($6::date + 1)::timestamp AT TIME ZONE 'UTC')`

const auditSelect = `
SELECT a.event_id, a.seq, a.action, a.resource_type, a.resource_id, a.actor_id,
       u.name AS actor_name, a.occurred_at, a.request_id, a.details,
       rw.id AS workflow_id, w.name AS workflow_name, a.hash` +
	auditFrom + `
ORDER BY a.occurred_at DESC, a.seq DESC
LIMIT $7 OFFSET $8`

const auditCount = `SELECT COUNT(*)::int` + auditFrom

func (r *AuditLogRepo) List(ctx context.Context, args domain.ListArgs) ([]domain.Entry, int, error) {
	from, to := dateArg(args.From), dateArg(args.To)

	entries := []domain.Entry{}
	if err := r.db.SelectContext(ctx, &entries, auditSelect,
		args.WorkflowID, args.ResourceType, args.ResourceID, args.Action, from, to,
		args.Limit, args.Offset); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total, auditCount,
		args.WorkflowID, args.ResourceType, args.ResourceID, args.Action, from, to); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// dateArg renders an optional date as a nullable text parameter for a
// `$n::date` cast (a nil *dateonly.Date would otherwise hit the Valuer on a
// nil receiver).
func dateArg(d *dateonly.Date) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}
