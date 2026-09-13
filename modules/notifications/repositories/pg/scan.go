// Package pg is the notifications module's Postgres side: the two reads the
// deadline-reminder scan needs.
//
// There is no write here, and that is worth saying out loud. A producer's only
// write is the outbox row, and it goes through platform/outbox on the caller's
// transaction — one enqueue, one implementation, shared by every producer in
// the system — so this package would gain nothing but a second copy of it.
package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

var (
	_ domain.TenantDayLister = (*ScanRepo)(nil)
	_ domain.DueTaskLister   = (*ScanRepo)(nil)
)

// ScanRepo is the reminder scan's two reads: which tenants are in which civil
// day, and one tenant's window of open work.
//
// They run in different scopes and that is not an accident. The tenant list is
// CONTROL PLANE — the tenants registry carries no RLS (ADR-0005), so the job
// may enumerate it without borrowing anyone's identity. The task read is TENANT
// DATA and runs on a transaction bound to one tenant, where RLS confines it.
// The job never holds a cross-tenant view of customer data at any moment.
type ScanRepo struct {
	db database.ExecerPg
}

func NewScanRepo(db database.ExecerPg) *ScanRepo {
	return &ScanRepo{db: db}
}

// tenantDaysSQL resolves each active tenant's civil day from its IANA zone.
//
// Postgres does the conversion, not Go, for two reasons. It is the SAME tz
// database the compliance reports already read "today" from (see the reports'
// tenantToday), so the digest and the dashboard can never disagree about
// whether a task is overdue; and a scratch container has no tzdata, so a Go
// time.LoadLocation would be a deployment trap for a job whose whole subject is
// timezones.
//
// The instant is $1 rather than NOW() so the job's clock is injectable.
//
// The LEFT JOIN on pg_timezone_names is the fail-safe: `AT TIME ZONE 'Mars/X'`
// RAISES, and one tenant with a corrupt zone would abort the statement and
// silence every other tenant's deadlines for the whole pass. The write paths
// validate the zone (PUT /tenant, platform/seed), so this should never fire —
// but the cost of being wrong about that is every customer's reminders, and the
// cost of the guard is one hash join against a small set function.
//
// Three values come back per tenant because the job needs three different
// things from the same conversion: the DATE (what "today" means for the due
// comparison, and the dedupe key), the HOUR (has the send boundary passed), and
// the INSTANT the day began (so due_at can say when the digest was owed rather
// than when it happened to be queued).
const tenantDaysSQL = `
SELECT t.id                                                         AS tenant_id,
       COALESCE(z.name, 'UTC')                                      AS zone,
       ($1::timestamptz AT TIME ZONE COALESCE(z.name, 'UTC'))::date AS local_date,
       EXTRACT(HOUR FROM ($1::timestamptz AT TIME ZONE COALESCE(z.name, 'UTC')))::int AS local_hour,
       (date_trunc('day', $1::timestamptz AT TIME ZONE COALESCE(z.name, 'UTC'))
            AT TIME ZONE COALESCE(z.name, 'UTC'))                   AS local_midnight
FROM tenants t
LEFT JOIN pg_timezone_names z ON z.name = t.timezone
WHERE t.status = 'active'
ORDER BY t.id`

func (r *ScanRepo) ListTenantDays(ctx context.Context, at time.Time) ([]domain.TenantDay, error) {
	var days []domain.TenantDay
	if err := r.db.SelectContext(ctx, &days, tenantDaysSQL, at.UTC()); err != nil {
		return nil, fmt.Errorf("notifications: list tenant days: %w", err)
	}
	return days, nil
}

// dueTasksSQL is one tenant's reminder window: open work, with someone to tell.
//
// The rules, each of which is a product decision rather than a filter:
//
//   - ti.status <> 'completed' — the same "open work" the dashboard counts.
//     A completed task is never overdue, whatever its due date.
//   - w.status <> 'archived' — an archived workflow is shelved work; nagging
//     about it is how a reminder mail becomes noise people filter.
//   - the INNER JOIN to users is the assignee rule: an UNASSIGNED task reaches
//     nobody. That is the prototype's behaviour, kept deliberately (see
//     ReminderSettings), and it is the first gap a preference should close —
//     unassigned overdue work arguably belongs to the tenant's admins.
//   - u.status = 'active' AND u.kind = 'human' — a disabled member and a
//     service account are not people to mail. A service account has a synthetic
//     internal address that no mailbox is behind.
//   - due_date <= local date + lead days — the window, in the TENANT'S day. The
//     lower end is open on purpose: however old an arrear is, it stays in the
//     digest until it is closed.
//
// users is not RLS-scoped (login must find an account before any tenant
// context exists), so the join is pinned to the row's tenant: a foreign uuid in
// assignee_id resolves to no row, never to another tenant's person.
//
// The predicate is served by idx_task_instances_open_due
// (tenant_id, due_date, order_index, id) WHERE status <> 'completed' — the
// partial index ADR-0026's paging work already added for exactly this shape.
const dueTasksSQL = `
SELECT ti.id            AS task_id,
       ti.workflow_id   AS workflow_id,
       ti.name          AS name,
       ti.period_code   AS period_code,
       ti.due_date      AS due_date,
       COALESCE(e.name, '') AS entity_name,
       w.name           AS workflow_name,
       ti.assignee_id   AS assignee_id,
       u.name           AS assignee_name,
       u.email          AS assignee_email
FROM task_instances ti
JOIN workflows w ON w.id = ti.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
JOIN users u ON u.id = ti.assignee_id AND u.tenant_id = ti.tenant_id
WHERE ti.status <> 'completed'
  AND w.status <> 'archived'
  AND u.status = 'active'
  AND u.kind = 'human'
  AND ti.due_date <= ($1::date + $2::int)
ORDER BY u.id, ti.due_date, ti.name, ti.id`

func (r *ScanRepo) ListDueTasks(ctx context.Context, localDate dateonly.Date, leadDays int) ([]domain.DueTask, error) {
	var tasks []domain.DueTask
	if err := r.db.SelectContext(ctx, &tasks, dueTasksSQL, localDate, leadDays); err != nil {
		return nil, fmt.Errorf("notifications: list due tasks: %w", err)
	}
	return tasks, nil
}
