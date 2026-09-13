package domain

import (
	"context"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The deadline reminder: what it is, and the two things the prototype it
// replaces got wrong.
//
// zentax-ui/server/notifications.ts was the product statement — scan the open
// task instances, tell the assignee about anything overdue or due within a
// week — and it is the behaviour reproduced here. Two of its rules are not:
//
//  1. THE DAY IS THE TENANT'S, NOT THE SERVER'S. The prototype compared a due
//     date against the Node process's midnight. Every deadline in this product
//     is a legal date (ADR-0002) whose "today" is the tenant's civil day
//     (ADR-0003, ADR-0023 §6) — the same definition the compliance reports
//     already use for missed/overdue. A tenant in Auckland and a tenant in
//     Honolulu are 22 hours apart; a server-day scan tells one of them a task
//     is overdue while their working day has not started, and tells the other
//     nothing until theirs is nearly over.
//
//  2. NOBODY IS TOLD THE SAME THING TWICE. The prototype sent one mail per task
//     per pass, so a restart, an overlapping pass or a second deployment meant
//     a second copy, and a preparer with forty overdue tasks got forty mails.
//     Here a person gets ONE digest per tenant-local day, and the dedupe key
//     (see DigestDedupeKey) makes re-running the scan a no-op.
//
// A third rule is added, and it is a product fix rather than a refactor: the
// prototype's window was `daysUntilDue <= 7 AND > 0`, which skips a task due
// TODAY — the one day it matters most. Due-today is its own bucket.

// Bucket is which side of the deadline a task sits on, in the tenant's day.
type Bucket string

const (
	// BucketOverdue: the due date has passed in the tenant's civil day.
	BucketOverdue Bucket = "overdue"
	// BucketToday: the due date IS the tenant's civil day.
	BucketToday Bucket = "due_today"
	// BucketSoon: due within the reminder lead time.
	BucketSoon Bucket = "due_soon"
)

// ReminderSettings is the policy of the scan. Every value here is a DEFAULT
// standing in for a preference that does not exist yet: there are no
// notification-preference tables on this side of the product, so rather than
// invent a preferences system the shape is kept where a preference would later
// read from — one struct, resolved per scan, that a per-member or per-tenant
// row can override without touching the job.
//
// What a preference would change, once there is somewhere to store one:
//
//	LeadDays   a person who wants a fortnight's warning, or only same-day.
//	SendHour   the hour of the working day a digest should land in.
//	(new)      opting out entirely, per notification kind.
//	(new)      routing UNASSIGNED overdue work to tenant admins — today it is
//	           reported to nobody, because the prototype had no rule for it and
//	           inventing an escalation is a product decision, not a default.
type ReminderSettings struct {
	// LeadDays is how far ahead of the due date a task first appears in the
	// digest. 7 is the prototype's window.
	LeadDays int
	// SendHour is the hour of the TENANT'S civil day (1-23) at which its digest
	// becomes due. Before it, the tenant is not scanned at all: a digest that
	// lands at 03:00 local is a digest nobody reads. 8 is the start of a
	// working day. Midnight is deliberately not selectable — 0 means "unset",
	// and an hour that means "the moment the day rolls over" is exactly the
	// behaviour the boundary exists to prevent.
	SendHour int
	// MaxTasksPerBucket bounds what one mail lists. A member with forty overdue
	// tasks needs the count and the worst of them, not forty lines; the rest
	// are named by a "and N more" the template renders from the omitted count.
	MaxTasksPerBucket int
}

// Reminder defaults. They are the prototype's rules where it had one.
const (
	DefaultLeadDays          = 7
	DefaultSendHour          = 8
	DefaultMaxTasksPerBucket = 20
)

// Normalize fills in any unset field and clamps the rest into range, so a
// zero-valued Settings is the documented default rather than a scan that
// notifies nobody (LeadDays 0) at midnight.
func (s ReminderSettings) Normalize() ReminderSettings {
	if s.LeadDays <= 0 {
		s.LeadDays = DefaultLeadDays
	}
	if s.SendHour <= 0 || s.SendHour > 23 {
		s.SendHour = DefaultSendHour
	}
	if s.MaxTasksPerBucket <= 0 {
		s.MaxTasksPerBucket = DefaultMaxTasksPerBucket
	}
	return s
}

// TenantDay is one active tenant together with the civil day it is currently
// in. Both fields are resolved by Postgres from the tenant's IANA zone, so the
// scan's notion of "today" is byte-for-byte the reports' tenantToday rather
// than a second implementation in Go that could drift from it.
type TenantDay struct {
	TenantID string `db:"tenant_id"`
	// Zone is the IANA name actually used (see the repository: an unknown zone
	// resolves to UTC rather than aborting every other tenant's scan).
	Zone string `db:"zone"`
	// LocalDate is the tenant's calendar date — the date every due date in the
	// scan is compared against, and the date that keys the digest's dedupe.
	LocalDate dateonly.Date `db:"local_date"`
	// LocalHour is 0-23 in the tenant's zone, checked against SendHour.
	LocalHour int `db:"local_hour"`
	// LocalMidnight is the INSTANT at which this civil day began — resolved by
	// Postgres from the same zone, so the job never needs Go's tz database (a
	// scratch container has none). SendHour hours past it is when the digest
	// was owed, which is the outbox's due_at: a pass that runs late because the
	// process was down then records HOW late, instead of claiming the message
	// only became due when it happened to be queued.
	LocalMidnight time.Time `db:"local_midnight"`
}

// DueTask is one open task instance inside the reminder window, joined to the
// person responsible for it. It is the row the digest is assembled from and,
// once assembled, the values that are frozen into the outbox payload.
type DueTask struct {
	TaskID     string        `db:"task_id"`
	WorkflowID string        `db:"workflow_id"`
	Name       string        `db:"name"`
	PeriodCode string        `db:"period_code"`
	DueDate    dateonly.Date `db:"due_date"`
	// EntityName is empty for a project workflow, which has no entity.
	EntityName string `db:"entity_name"`
	// WorkflowName is the fallback context line for exactly that case.
	WorkflowName string `db:"workflow_name"`

	AssigneeID    string `db:"assignee_id"`
	AssigneeName  string `db:"assignee_name"`
	AssigneeEmail string `db:"assignee_email"`
}

// TenantDayLister enumerates the tenants a scan should consider, each with its
// own current civil day.
type TenantDayLister interface {
	// ListTenantDays returns every ACTIVE tenant as of the given instant. The
	// instant is a parameter rather than NOW() so the job's clock is
	// injectable — the tenant-boundary rule is a statement about time, and a
	// test that cannot choose the time cannot state it.
	ListTenantDays(ctx context.Context, at time.Time) ([]TenantDay, error)
}

// DueTaskLister reads one tenant's reminder window. It runs on a transaction
// already bound to that tenant, so RLS — not a WHERE clause — is what keeps it
// inside it.
type DueTaskLister interface {
	// ListDueTasks returns the tenant's open, assigned task instances due on or
	// before localDate + leadDays, ordered by recipient then due date.
	ListDueTasks(ctx context.Context, localDate dateonly.Date, leadDays int) ([]DueTask, error)
}
