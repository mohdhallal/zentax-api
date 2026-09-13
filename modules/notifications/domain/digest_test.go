package domain_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The tenant's civil day, fixed, so every case below reads as arithmetic
// against a known "today" rather than against the machine's clock.
func today() domain.TenantDay {
	return domain.TenantDay{
		TenantID:      "11111111-1111-1111-1111-111111111111",
		Zone:          "Europe/Berlin",
		LocalDate:     dateonly.New(2026, 9, 13),
		LocalHour:     9,
		LocalMidnight: time.Date(2026, 9, 12, 22, 0, 0, 0, time.UTC),
	}
}

func task(id string, due dateonly.Date, assignee string) domain.DueTask {
	return domain.DueTask{
		TaskID:        id,
		WorkflowID:    "wf-" + id,
		Name:          "Task " + id,
		PeriodCode:    "M1",
		DueDate:       due,
		EntityName:    "Acme GmbH",
		WorkflowName:  "Monthly VAT",
		AssigneeID:    assignee,
		AssigneeName:  "Assignee " + assignee,
		AssigneeEmail: assignee + "@acme.test",
	}
}

// TestBucketsAreTheTenantsDay is the rule the prototype got wrong twice: which
// side of the deadline a task is on is decided against the TENANT'S date, and a
// task due TODAY is its own bucket rather than falling through the gap between
// "overdue" and "due in 1..7 days".
func TestBucketsAreTheTenantsDay(t *testing.T) {
	t.Parallel()

	day := today()
	tasks := []domain.DueTask{
		task("late", dateonly.New(2026, 8, 30), "u1"), // 14 days overdue
		task("yesterday", dateonly.New(2026, 9, 12), "u1"),
		task("today", dateonly.New(2026, 9, 13), "u1"),
		task("tomorrow", dateonly.New(2026, 9, 14), "u1"),
		task("edge", dateonly.New(2026, 9, 20), "u1"),   // exactly lead days away
		task("beyond", dateonly.New(2026, 9, 21), "u1"), // one day past the window
	}

	digests := domain.BuildDigests(day, tasks, domain.ReminderSettings{})
	require.Len(t, digests, 1)
	p := digests[0].Payload

	assert.Equal(t, 2, p.Overdue.Count, "both past-dated tasks are overdue")
	assert.Equal(t, 1, p.DueToday.Count, "the task due today is neither overdue nor merely soon")
	assert.Equal(t, 2, p.DueSoon.Count, "tomorrow and the lead-day edge are in the window")
	assert.Equal(t, 5, p.TotalTasks, "the task a day past the window is in no bucket at all")

	// Overdue lists the oldest arrear first, and says how late it is as a
	// negative number of days — one signed field, so a template cannot print a
	// positive number next to the word "overdue".
	require.Len(t, p.Overdue.Tasks, 2)
	assert.Equal(t, "late", p.Overdue.Tasks[0].TaskID)
	assert.Equal(t, -14, p.Overdue.Tasks[0].DaysUntilDue)
	assert.Equal(t, -1, p.Overdue.Tasks[1].DaysUntilDue)
	assert.Equal(t, 0, p.DueToday.Tasks[0].DaysUntilDue)
	assert.Equal(t, 1, p.DueSoon.Tasks[0].DaysUntilDue)
	assert.Equal(t, 7, p.DueSoon.Tasks[1].DaysUntilDue)
}

// TestOneDigestPerPerson is the reason the digest exists: forty overdue tasks
// are forty LINES (capped), not forty messages.
func TestOneDigestPerPerson(t *testing.T) {
	t.Parallel()

	day := today()
	var tasks []domain.DueTask
	for i := range 40 {
		tasks = append(tasks, task("t"+strconv.Itoa(i), dateonly.New(2026, 8, 1), "u1"))
	}

	digests := domain.BuildDigests(day, tasks, domain.ReminderSettings{})
	require.Len(t, digests, 1, "one person, one message")

	p := digests[0].Payload
	assert.Equal(t, 40, p.Overdue.Count, "the count is the truth")
	assert.Len(t, p.Overdue.Tasks, domain.DefaultMaxTasksPerBucket, "the list is bounded")
	assert.Equal(t, 40-domain.DefaultMaxTasksPerBucket, p.Overdue.Omitted,
		"and says how many it left out, so the template need not do arithmetic")
}

// TestOneDigestPerRecipient: several people, one message each, addressed to the
// person the task is assigned to.
func TestOneDigestPerRecipient(t *testing.T) {
	t.Parallel()

	day := today()
	tasks := []domain.DueTask{
		task("a", dateonly.New(2026, 9, 10), "u1"),
		task("b", dateonly.New(2026, 9, 14), "u2"),
		task("c", dateonly.New(2026, 9, 13), "u1"),
	}

	digests := domain.BuildDigests(day, tasks, domain.ReminderSettings{})
	require.Len(t, digests, 2)
	assert.Equal(t, "u1", digests[0].Recipient.UserID)
	assert.Equal(t, "u1@acme.test", digests[0].Recipient.Email)
	assert.Equal(t, 2, digests[0].Payload.TotalTasks)
	assert.Equal(t, "u2", digests[1].Recipient.UserID)
	assert.Equal(t, 1, digests[1].Payload.TotalTasks)
}

// TestNobodyToTellProducesNothing: a task with no assignee or no address is not
// a message addressed to nobody — it is no message.
func TestNobodyToTellProducesNothing(t *testing.T) {
	t.Parallel()

	unassigned := task("a", dateonly.New(2026, 9, 10), "")
	noAddress := task("b", dateonly.New(2026, 9, 10), "u2")
	noAddress.AssigneeEmail = ""

	digests := domain.BuildDigests(today(), []domain.DueTask{unassigned, noAddress}, domain.ReminderSettings{})
	assert.Empty(t, digests)
}

// TestEmptyWindowProducesNoMail: a person whose only task is beyond the window
// gets no digest rather than an empty one.
func TestEmptyWindowProducesNoMail(t *testing.T) {
	t.Parallel()

	digests := domain.BuildDigests(today(),
		[]domain.DueTask{task("far", dateonly.New(2026, 12, 1), "u1")},
		domain.ReminderSettings{})
	assert.Empty(t, digests)
}

// TestProjectWorkflowContextFallsBackToTheWorkflow: a project workflow has no
// entity, so the line names the workflow rather than showing an empty column.
func TestProjectWorkflowContextFallsBackToTheWorkflow(t *testing.T) {
	t.Parallel()

	project := task("p", dateonly.New(2026, 9, 13), "u1")
	project.EntityName = ""
	project.WorkflowName = "Transfer-pricing dispute"

	digests := domain.BuildDigests(today(), []domain.DueTask{project}, domain.ReminderSettings{})
	require.Len(t, digests, 1)
	assert.Equal(t, "Transfer-pricing dispute", digests[0].Payload.DueToday.Tasks[0].Context)
}

// TestSettingsNormalize: a zero-valued Settings is the documented default, not
// a scan that notifies nobody at midnight.
func TestSettingsNormalize(t *testing.T) {
	t.Parallel()

	got := domain.ReminderSettings{}.Normalize()
	assert.Equal(t, domain.DefaultLeadDays, got.LeadDays)
	assert.Equal(t, domain.DefaultSendHour, got.SendHour)
	assert.Equal(t, domain.DefaultMaxTasksPerBucket, got.MaxTasksPerBucket)

	out := domain.ReminderSettings{LeadDays: 14, SendHour: 25, MaxTasksPerBucket: 5}.Normalize()
	assert.Equal(t, 14, out.LeadDays)
	assert.Equal(t, domain.DefaultSendHour, out.SendHour, "an out-of-range hour falls back")
	assert.Equal(t, 5, out.MaxTasksPerBucket)
}

// TestDigestDedupeKeyIsThePersonsDay: the key is stable within a tenant-local
// day and changes with it — which is the whole once-a-day guarantee, expressed
// as a string Postgres can hold unique.
func TestDigestDedupeKeyIsThePersonsDay(t *testing.T) {
	t.Parallel()

	day := dateonly.New(2026, 9, 13)
	assert.Equal(t, "reminder:2026-09-13:u1", domain.DigestDedupeKey(day, "u1"))
	assert.Equal(t, domain.DigestDedupeKey(day, "u1"), domain.DigestDedupeKey(day, "u1"))
	assert.NotEqual(t, domain.DigestDedupeKey(day, "u1"), domain.DigestDedupeKey(day, "u2"))
	assert.NotEqual(t, domain.DigestDedupeKey(day, "u1"),
		domain.DigestDedupeKey(dateonly.New(2026, 9, 14), "u1"))
}

// TestSummaryFieldsCarryNoPersonalData: the string the reminder job's
// per-digest log line is built from is counts. A recipient, a task name and an
// entity name are all data a log must not hold (ADR-0015, ADR-0027 decision 9).
//
// This is the unit half of that claim, and on its own it would be worth little:
// a helper nobody called could satisfy it forever. The half that can actually
// fail is in usecases — TestDigestLogLineIsCountsOnly runs a real pass and reads
// back what it wrote to the log, so a call site that starts naming the person
// breaks a test even though this one still passes.
func TestSummaryFieldsCarryNoPersonalData(t *testing.T) {
	t.Parallel()

	// The fixture's task carries an entity ("Acme GmbH"), a task name and an
	// address, so the negative assertions below have something to catch.
	digests := domain.BuildDigests(today(),
		[]domain.DueTask{task("a", dateonly.New(2026, 9, 10), "u1")},
		domain.ReminderSettings{})
	require.Len(t, digests, 1)

	summary := digests[0].Payload.SummaryFields()
	assert.Equal(t, "overdue=1 today=0 soon=0", summary)
	assert.NotContains(t, summary, "@")
	assert.NotContains(t, summary, "Acme")
	assert.NotContains(t, summary, "Task a")
	assert.NotContains(t, summary, "Assignee")
}
