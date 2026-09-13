package usecases

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
)

// The deadline reminder job.
//
// This is the scheduled producer: once a pass, it asks every active tenant
// whether its civil day has reached the hour a digest is owed, and for those
// that have, queues one message per person with open work at or past its
// deadline. It sends nothing; it writes outbox rows.
//
// Three properties are the whole design, and each is enforced somewhere other
// than "the job remembers":
//
//	the tenant's day    Postgres resolves each tenant's civil date, hour and
//	                    midnight from its IANA zone in ONE statement — the same
//	                    tz database the compliance reports' tenantToday uses.
//	                    A tenant still in the small hours is skipped, not
//	                    scanned early.
//	once per day        the dedupe key is (tenant-local date, user), and its
//	                    uniqueness is a Postgres index. Run the pass ten times,
//	                    restart the process between them, run two schedulers
//	                    that both think they hold the lease: one digest.
//	one mail per person the scan groups by assignee before it enqueues, so forty
//	                    overdue tasks are forty LINES, not forty messages.
//
// It is safe to call concurrently with itself and safe to call at any cadence;
// hourly is the intended one, because SendHour is a tenant-local hour and a
// pass that only ran daily at a fixed UTC instant would serve exactly one
// timezone well.
type ReminderJob struct {
	tx       database.ExecerPgTx
	tenants  domain.TenantDayLister
	tasks    domain.DueTaskLister
	outbox   domain.Enqueuer
	settings domain.ReminderSettings
	now      func() time.Time
}

func NewReminderJob(
	tx database.ExecerPgTx,
	tenants domain.TenantDayLister,
	tasks domain.DueTaskLister,
	outbox domain.Enqueuer,
	settings domain.ReminderSettings,
) *ReminderJob {
	return &ReminderJob{
		tx: tx, tenants: tenants, tasks: tasks, outbox: outbox,
		settings: settings.Normalize(),
		now:      time.Now,
	}
}

// WithClock replaces the job's clock. The boundary rule is a claim about time,
// so the tests that state it choose the instant.
func (j *ReminderJob) WithClock(now func() time.Time) *ReminderJob {
	if now != nil {
		j.now = now
	}
	return j
}

// Name identifies the job to a scheduler and to metrics.
func (j *ReminderJob) Name() string { return "notifications.deadline-reminder" }

// ReminderReport is what one pass did. Counts only — no tenant is named beyond
// its id and no recipient at all, so the whole report is safe to log.
type ReminderReport struct {
	// TenantsConsidered is every active tenant the pass looked at.
	TenantsConsidered int
	// TenantsEarly is those whose civil day had not yet reached SendHour. They
	// are not a failure: they are the reason the pass runs hourly.
	TenantsEarly int
	// TenantsScanned is those actually scanned.
	TenantsScanned int
	// TenantsFailed is those whose scan errored. One tenant's failure never
	// stops another's — see Run.
	TenantsFailed int
	// Recipients is how many people had work in the window.
	Recipients int
	// Queued is digests newly written; Suppressed is digests this pass
	// assembled and the dedupe key rejected, i.e. already owed today. On a
	// second run of the same day Queued is 0 and Suppressed is the population.
	Queued     int
	Suppressed int
}

// Run executes one pass. It returns what it did even when some tenants failed:
// the error is the joined tenant failures, and the report is still the truth
// about the rest.
func (j *ReminderJob) Run(ctx context.Context) (ReminderReport, error) {
	var report ReminderReport
	at := j.now().UTC()

	days, err := j.tenants.ListTenantDays(ctx, at)
	if err != nil {
		return report, fmt.Errorf("notifications: list tenant days: %w", err)
	}
	report.TenantsConsidered = len(days)

	var failures []error
	for _, day := range days {
		// The boundary. A digest that lands at 03:00 local is a digest nobody
		// reads, and — worse for a compliance product — a "today" that has not
		// started yet is a different day's answer.
		if day.LocalHour < j.settings.SendHour {
			report.TenantsEarly++
			continue
		}
		tenantReport, err := j.scanTenant(ctx, day)
		if err != nil {
			// One tenant's bad data or lock contention must not silence every
			// other tenant's deadlines. Record it, log it, keep going.
			report.TenantsFailed++
			failures = append(failures, fmt.Errorf("tenant %s: %w", day.TenantID, err))
			logIfConfigured(ctx).Error("notifications: reminder scan failed for a tenant",
				logger.String("tenantId", day.TenantID), logger.Error(err))
			continue
		}
		report.TenantsScanned++
		report.Recipients += tenantReport.Recipients
		report.Queued += tenantReport.Queued
		report.Suppressed += tenantReport.Suppressed
	}

	logIfConfigured(ctx).Info("notifications: reminder pass complete",
		logger.Int("tenantsConsidered", report.TenantsConsidered),
		logger.Int("tenantsEarly", report.TenantsEarly),
		logger.Int("tenantsScanned", report.TenantsScanned),
		logger.Int("tenantsFailed", report.TenantsFailed),
		logger.Int("recipients", report.Recipients),
		logger.Int("queued", report.Queued),
		logger.Int("suppressed", report.Suppressed))

	return report, errors.Join(failures...)
}

// tenantScan is one tenant's contribution to the report.
type tenantScan struct {
	Recipients int
	Queued     int
	Suppressed int
}

// scanTenant reads one tenant's window and queues its digests, all on ONE
// transaction bound to that tenant.
//
// The binding is the isolation: the job holds no tenant of its own, so it
// borrows each one in turn through the same Tx seam every request uses
// (app.tenant_id via SET LOCAL), and Postgres RLS — not a WHERE clause the job
// could forget — is what keeps the read and the writes inside that tenant. The
// context carries no requester, so the outbox row's created_by is NULL: nobody
// asked for this message; a schedule did.
//
// One transaction per tenant, not one per digest: the pass either owes this
// tenant its whole day or none of it, and the digest count is bounded by the
// number of PEOPLE in the tenant, not by its tasks.
func (j *ReminderJob) scanTenant(ctx context.Context, day domain.TenantDay) (tenantScan, error) {
	var scan tenantScan
	tenantCtx := app.WithTenantID(ctx, day.TenantID)

	err := j.tx.WithinTransaction(tenantCtx, func(ctx context.Context) error {
		tasks, err := j.tasks.ListDueTasks(ctx, day.LocalDate, j.settings.LeadDays)
		if err != nil {
			return fmt.Errorf("list due tasks: %w", err)
		}
		digests := domain.BuildDigests(day, tasks, j.settings)
		scan.Recipients = len(digests)

		dueAt := day.LocalMidnight.Add(time.Duration(j.settings.SendHour) * time.Hour)
		for _, digest := range digests {
			payload, err := domain.Payload(digest.Payload)
			if err != nil {
				return err
			}
			receipt, err := j.outbox.Enqueue(ctx, outbox.NewMessage{
				Template:  domain.TemplateDeadlineReminder,
				Recipient: digest.Recipient.Email,
				Payload:   payload,
				DedupeKey: domain.DigestDedupeKey(day.LocalDate, digest.Recipient.UserID),
				DueAt:     dueAt,
			})
			if err != nil {
				return fmt.Errorf("queue digest: %w", err)
			}
			if receipt.Deduplicated {
				scan.Suppressed++
			} else {
				scan.Queued++
			}
			// One line per digest, at debug — because the pass-level counters
			// logged by Run cannot answer the question an operator actually
			// arrives with: "this person says they were told nothing; was a
			// message written for them, and what was in it?" The message id
			// answers the first half and is the join into the outbox row.
			//
			// The second half can only ever be COUNTS. The recipient, the task
			// names and the entity names are personal or customer data, and a
			// log sits outside the erasure boundary (ADR-0015; ADR-0027
			// decision 9), so SummaryFields is the only description of a digest
			// this job may emit — and reminders_test.go asserts that against
			// what a real pass writes, not against the helper in isolation.
			logIfConfigured(ctx).Debug("notifications: digest queued",
				logger.String("tenantId", day.TenantID),
				logger.String("messageId", receipt.ID),
				logger.Bool("deduplicated", receipt.Deduplicated),
				logger.String("digest", digest.Payload.SummaryFields()))
		}
		return nil
	})
	if err != nil {
		return tenantScan{}, err
	}
	return scan, nil
}

// loggerInit guards the fallback below: the job may be constructed directly by
// a unit test, which never went through cmd/server's logger.Init, and two
// concurrent passes must not race to install one.
var loggerInit sync.Once

// logIfConfigured returns the process logger, initialising a default one if
// nothing has. A job must not panic on its first log line.
func logIfConfigured(ctx context.Context) logger.Logger {
	loggerInit.Do(func() {
		if logger.Log == nil {
			logger.InitBasic()
		}
	})
	return logger.Log.WithContext(ctx)
}
