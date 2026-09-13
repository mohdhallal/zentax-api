package notifications_test

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	notificationsdomain "github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	notificationspg "github.com/mohamadhallal/zentax-api/modules/notifications/repositories/pg"
	notificationsusecases "github.com/mohamadhallal/zentax-api/modules/notifications/usecases"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// scanInstant is the moment every reminder case below runs at. It is chosen so
// that one instant puts two tenants on opposite sides of the send boundary:
//
//	10:00 UTC  =  12:00 in Europe/Berlin (CEST, UTC+2)  → past 08:00, scan
//	           =  00:00 in Pacific/Honolulu (UTC-10)    → before 08:00, do not
//
// Neither zone's offset is in doubt on this date: Berlin is on summer time
// until late October, and Honolulu has no DST at all. The job's clock is
// injected, so the assertions are about the tenant's civil day rather than
// about when the suite happened to run.
var scanInstant = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// berlinToday / honoluluToday are those two tenants' civil dates at that
// instant — both 2026-09-13, which is what makes the test about the HOUR.
var berlinToday = dateonly.New(2026, 9, 13)

// reminderJob builds the job exactly as bootstrap does, on the suite's pool,
// with its clock pinned to scanInstant.
func (s *NotificationsSuite) reminderJob() *notificationsusecases.ReminderJob {
	db := database.NewExec(s.DB)
	return notificationsusecases.NewReminderJob(
		db,
		notificationspg.NewScanRepo(db),
		notificationspg.NewScanRepo(db),
		outbox.NewQueue(db),
		notificationsdomain.ReminderSettings{},
	).WithClock(func() time.Time { return scanInstant })
}

// seedAssignee inserts an active human member of the tenant (users is not
// RLS-scoped, so the tenant is explicit) and returns its id.
func (s *NotificationsSuite) seedAssignee(tenantID, email, name string) string {
	var id string
	s.Require().NoError(s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status, kind)
		 VALUES ($1, $2, $3, 'active', 'human') RETURNING id`,
		tenantID, email, name).Scan(&id))
	return id
}

// seedWorkflow inserts an entity + an active recurring workflow + one task
// template, and returns the workflow and template ids. The reminder scan reads
// the entity's name for the digest line, so the entity is not decoration.
func (s *NotificationsSuite) seedWorkflow(tenantID, entityName, workflowName string) (workflowID, taskID string) {
	s.withTenantCommit(tenantID, func(tx *sqlx.Tx) {
		var entityID string
		s.Require().NoError(tx.QueryRowx(
			`INSERT INTO entities (name, country) VALUES ($1, 'Germany') RETURNING id`,
			entityName).Scan(&entityID))
		s.Require().NoError(tx.QueryRowx(
			`INSERT INTO workflows (name, workflow_category, entity_id, status)
			 VALUES ($1, 'recurring', $2, 'active') RETURNING id`,
			workflowName, entityID).Scan(&workflowID))
		s.Require().NoError(tx.QueryRowx(
			`INSERT INTO workflow_tasks (workflow_id, name, task_type)
			 VALUES ($1, 'Prepare', 'preparation') RETURNING id`,
			workflowID).Scan(&taskID))
	})
	return workflowID, taskID
}

// seedTask inserts one task instance due on the given date, assigned to
// assigneeID.
func (s *NotificationsSuite) seedTask(tenantID, workflowID, workflowTaskID, assigneeID, name string, due dateonly.Date, status string) {
	s.withTenantCommit(tenantID, func(tx *sqlx.Tx) {
		_, err := tx.Exec(
			`INSERT INTO task_instances
			   (workflow_id, workflow_task_id, period_code, name, task_type, status,
			    assignee_id, due_date, period_end_date, filing_deadline)
			 VALUES ($1, $2, 'M8', $3, 'preparation', $4, $5, $6, $6, $6)`,
			workflowID, workflowTaskID, name, status, assigneeID, due.String())
		s.Require().NoError(err)
	})
}

// withTenantCommit is withTenant that keeps its writes.
func (s *NotificationsSuite) withTenantCommit(tenantID string, fn func(tx *sqlx.Tx)) {
	tx := s.DB.MustBegin()
	_, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	if err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	fn(tx)
	s.Require().NoError(tx.Commit())
}

// TestReminderScanQueuesOneDigestPerPersonPerDay is the second half of the
// wave, and it states all three properties at once against live Postgres:
//
//   - the digest is ONE message however many tasks a person has;
//   - running the scan twice in the same tenant-local day queues nothing the
//     second time (the dedupe key is a unique index, not a memory);
//   - a tenant whose civil day has not reached the send hour is not scanned.
func (s *NotificationsSuite) TestReminderScanQueuesOneDigestPerPersonPerDay() {
	berlin := s.InsertTenantWithTimezone("notif-berlin", "Berlin Tenant", "Europe/Berlin").String()
	honolulu := s.InsertTenantWithTimezone("notif-honolulu", "Honolulu Tenant", "Pacific/Honolulu").String()

	preparer := s.seedAssignee(berlin, "preparer@acme.test", "Paula Preparer")
	reviewer := s.seedAssignee(berlin, "reviewer@acme.test", "Ravi Reviewer")
	early := s.seedAssignee(honolulu, "early@island.test", "Early Bird")

	wf, task := s.seedWorkflow(berlin, "Acme GmbH", "Monthly VAT")
	// Paula: three tasks across all three buckets, plus one completed (never
	// overdue, whatever its date) and one beyond the window.
	s.seedTask(berlin, wf, task, preparer, "File August VAT", dateonly.New(2026, 8, 15), "not_started")
	s.seedTask(berlin, wf, task, preparer, "Reconcile ledger", berlinToday, "in_progress")
	s.seedTask(berlin, wf, task, preparer, "Prepare September VAT", dateonly.New(2026, 9, 18), "not_started")
	s.seedTask(berlin, wf, task, preparer, "Already filed", dateonly.New(2026, 7, 1), "completed")
	s.seedTask(berlin, wf, task, preparer, "Far future", dateonly.New(2026, 12, 1), "not_started")
	// Ravi: one overdue task, so he is a second recipient rather than a second
	// message to Paula.
	s.seedTask(berlin, wf, task, reviewer, "Review Q2 filing", dateonly.New(2026, 9, 10), "not_started")

	// Honolulu has work too — it is just still the middle of its night.
	hwf, htask := s.seedWorkflow(honolulu, "Island Holdings", "Annual CIT")
	s.seedTask(honolulu, hwf, htask, early, "File CIT", dateonly.New(2026, 8, 1), "not_started")

	job := s.reminderJob()

	first, err := job.Run(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(2, first.TenantsConsidered)
	s.Require().Equal(1, first.TenantsScanned)
	s.Require().Equal(1, first.TenantsEarly, "Honolulu is at 00:00 local: not due, not scanned")
	s.Require().Equal(2, first.Recipients)
	s.Require().Equal(2, first.Queued)
	s.Require().Equal(0, first.Suppressed)

	// --- Berlin got exactly one message per person -------------------------
	digests := s.outboxOf(berlin, notificationsdomain.TemplateDeadlineReminder)
	s.Require().Len(digests, 2, "two people with work, two messages")

	byRecipient := map[string]queuedMessage{}
	for _, m := range digests {
		byRecipient[m.Recipient] = m
	}
	paula, ok := byRecipient["preparer@acme.test"]
	s.Require().True(ok, "recipients: %v", byRecipient)

	s.Require().Nil(paula.CreatedBy, "a scheduled producer has no acting user (ADR-0008)")
	s.Require().Equal("reminder:2026-09-13:"+preparer, *paula.DedupeKey)
	// due_at is when the digest was OWED — 08:00 Berlin time, which is 06:00
	// UTC — not the moment the pass happened to run (10:00 UTC).
	s.Require().Contains(paula.DueAt, "06:00:00")

	payload := s.payloadOf(paula)
	s.Require().Equal("Paula Preparer", payload["recipientName"])
	s.Require().Equal("2026-09-13", payload["localDate"])
	s.Require().InDelta(3, payload["totalTasks"], 0, "three tasks in the window")
	s.Require().InDelta(1, section(payload, "overdue")["count"], 0)
	s.Require().InDelta(1, section(payload, "dueToday")["count"], 0)
	s.Require().InDelta(1, section(payload, "dueSoon")["count"], 0)

	overdue := section(payload, "overdue")["tasks"].([]any)[0].(map[string]any)
	s.Require().Equal("File August VAT", overdue["name"])
	s.Require().Equal("Acme GmbH", overdue["context"], "the digest line names the entity")
	s.Require().Equal("2026-08-15", overdue["dueDate"])
	s.Require().InDelta(-29, overdue["daysUntilDue"], 0, "29 tenant-days late")

	// --- Honolulu was not scanned at all -----------------------------------
	s.Require().Empty(s.outboxOf(honolulu, notificationsdomain.TemplateDeadlineReminder),
		"a tenant whose civil day has not reached the send hour is not reminded early")

	// --- The same day, again ------------------------------------------------
	second, err := job.Run(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(2, second.Recipients, "the same people are still owed a digest")
	s.Require().Equal(0, second.Queued, "but nothing new is written")
	s.Require().Equal(2, second.Suppressed)
	s.Require().Len(s.outboxOf(berlin, notificationsdomain.TemplateDeadlineReminder), 2,
		"one digest per person per day, however often the scan runs")
}

// TestReminderScanCrossesIntoTheNextTenantDay: the dedupe key is the tenant's
// DATE, so the day after is a new message rather than a suppressed duplicate —
// which is the other half of "not told the same thing twice".
func (s *NotificationsSuite) TestReminderScanCrossesIntoTheNextTenantDay() {
	tenant := s.InsertTenantWithTimezone("notif-nextday", "Next Day Tenant", "Europe/Berlin").String()
	assignee := s.seedAssignee(tenant, "nextday@acme.test", "Nina Next")
	wf, task := s.seedWorkflow(tenant, "Acme GmbH", "Monthly VAT")
	s.seedTask(tenant, wf, task, assignee, "File August VAT", dateonly.New(2026, 8, 15), "not_started")

	today, err := s.reminderJob().Run(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(1, today.Queued)

	tomorrow := s.reminderJob().WithClock(func() time.Time {
		return scanInstant.Add(24 * time.Hour)
	})
	next, err := tomorrow.Run(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(1, next.Queued, "a new civil day is a new digest")

	digests := s.outboxOf(tenant, notificationsdomain.TemplateDeadlineReminder)
	s.Require().Len(digests, 2)
	s.Require().Equal("reminder:2026-09-13:"+assignee, *digests[0].DedupeKey)
	s.Require().Equal("reminder:2026-09-14:"+assignee, *digests[1].DedupeKey)
}

// TestReminderScanTellsNobodyAboutWorkThatHasNobody: the digest goes to the
// ASSIGNEE, so unassigned work, work assigned to a disabled member, and work on
// an archived workflow all reach no mailbox. Each is a deliberate default, and
// the first is the one a notification preference should eventually change.
func (s *NotificationsSuite) TestReminderScanTellsNobodyAboutWorkThatHasNobody() {
	tenant := s.InsertTenantWithTimezone("notif-nobody", "Nobody Tenant", "Europe/Berlin").String()
	wf, task := s.seedWorkflow(tenant, "Acme GmbH", "Monthly VAT")

	// Unassigned.
	s.withTenantCommit(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(
			`INSERT INTO task_instances
			   (workflow_id, workflow_task_id, period_code, name, task_type, due_date, period_end_date, filing_deadline)
			 VALUES ($1, $2, 'M8', 'Nobody owns this', 'preparation', $3, $3, $3)`,
			wf, task, "2026-08-15")
		s.Require().NoError(err)
	})

	// Assigned to a member who has since been disabled.
	var disabled string
	s.Require().NoError(s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status, kind)
		 VALUES ($1, $2, 'Gone Away', 'disabled', 'human') RETURNING id`,
		tenant, "gone-"+uuid.NewString()+"@acme.test").Scan(&disabled))
	s.seedTask(tenant, wf, task, disabled, "Left the company", dateonly.New(2026, 8, 15), "not_started")

	// Assigned, but the workflow was archived.
	archived := s.seedAssignee(tenant, "archived@acme.test", "Archie Archived")
	awf, atask := s.seedWorkflow(tenant, "Acme GmbH", "Shelved project")
	s.withTenantCommit(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(`UPDATE workflows SET status = 'archived' WHERE id = $1`, awf)
		s.Require().NoError(err)
	})
	s.seedTask(tenant, awf, atask, archived, "Shelved work", dateonly.New(2026, 8, 15), "not_started")

	report, err := s.reminderJob().Run(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(1, report.TenantsScanned)
	s.Require().Equal(0, report.Recipients)
	s.Require().Equal(0, report.Queued)
	s.Require().Empty(s.outboxOf(tenant, notificationsdomain.TemplateDeadlineReminder))
}

// section reads one bucket out of a decoded digest payload.
func section(payload map[string]any, name string) map[string]any {
	s, _ := payload[name].(map[string]any)
	return s
}
