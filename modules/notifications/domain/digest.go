package domain

import (
	"sort"
	"strconv"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Assembling the digest: rows in, one message per person out.
//
// This file is pure. It takes the tenant's civil day and the tasks the scan
// read, and returns the messages that day owes — no clock, no database, no
// enqueue. That is what makes the two rules worth stating testable as
// arithmetic: which side of the deadline a task is on, and how many mails a
// person with forty overdue tasks receives (one).

// Recipient is the person a digest is addressed to.
type Recipient struct {
	UserID string
	Name   string
	Email  string
}

// Digest is one assembled message: who it goes to, and the frozen payload the
// mail template will render.
type Digest struct {
	Recipient Recipient
	Payload   DigestPayload
}

// DigestPayload is the value set of the deadline.reminder template.
//
// It is a SNAPSHOT: the runner renders from this and never re-reads the tasks,
// so a task completed between enqueue and delivery still appears. That is the
// honest behaviour for a queued message — it says what was true when it was
// owed — and it is why the payload carries display values (names, dates,
// counts) rather than ids the renderer would have to resolve against a tenant
// it is not acting for.
type DigestPayload struct {
	RecipientName string        `json:"recipientName"`
	LocalDate     dateonly.Date `json:"localDate"`
	LeadDays      int           `json:"leadDays"`
	// TotalTasks counts everything the three sections cover, including tasks
	// omitted by the per-section cap.
	TotalTasks int           `json:"totalTasks"`
	Overdue    DigestSection `json:"overdue"`
	DueToday   DigestSection `json:"dueToday"`
	DueSoon    DigestSection `json:"dueSoon"`
}

// DigestSection is one bucket of the digest.
type DigestSection struct {
	// Count is how many tasks are in the bucket in total.
	Count int `json:"count"`
	// Omitted is how many of them the cap left out of Tasks (Count - len(Tasks)),
	// so the template can render "and N more" without doing arithmetic.
	Omitted int `json:"omitted"`
	// Tasks are the listed ones, soonest deadline first.
	Tasks []DigestTask `json:"tasks"`
}

// DigestTask is one line of the digest.
type DigestTask struct {
	TaskID     string `json:"taskId"`
	WorkflowID string `json:"workflowId"`
	Name       string `json:"name"`
	// Context is the entity the work is for, falling back to the workflow's own
	// name for a project workflow (which has no entity).
	Context    string        `json:"context"`
	PeriodCode string        `json:"periodCode"`
	DueDate    dateonly.Date `json:"dueDate"`
	// DaysUntilDue is signed, in the TENANT'S civil days: 3 means "due in three
	// days", 0 "due today", -5 "five days overdue". One field rather than two
	// so a template cannot render a positive number next to the word "overdue".
	DaysUntilDue int `json:"daysUntilDue"`
}

// IsEmpty reports whether a section has nothing to say (so a template can skip
// the heading rather than print "Overdue (0)").
func (s DigestSection) IsEmpty() bool { return s.Count == 0 }

// DigestDedupeKey is the idempotency contract of the reminder scan: one digest
// per person per TENANT-LOCAL day.
//
// The date in the key is the tenant's civil date, not the server's, for the
// same reason the buckets are — it is the day the recipient is living in. Run
// the scan at 08:00, 09:00 and again after a restart at 14:00 and the key is
// identical all three times, so the second and third enqueues write nothing.
// Roll into the tenant's next day and the key changes, so tomorrow's digest is
// a new message rather than a suppressed duplicate.
//
// Its uniqueness is enforced by Postgres (uq_outbox_messages_dedupe on
// (tenant_id, dedupe_key)), not by the job reading first and writing after:
// two schedulers that both believed they held the lease would still produce
// exactly one row.
func DigestDedupeKey(localDate dateonly.Date, userID string) string {
	return "reminder:" + localDate.String() + ":" + userID
}

// BuildDigests turns one tenant's due tasks into the messages its civil day
// owes: one per assignee, tasks bucketed against that day.
//
// Recipients come back in first-appearance order and their tasks sorted by
// deadline — the caller's query already orders that way, and re-establishing it
// here keeps the function total: its output depends on its input, not on how
// the input was fetched.
func BuildDigests(day TenantDay, tasks []DueTask, settings ReminderSettings) []Digest {
	settings = settings.Normalize()

	order := make([]string, 0, 8)
	byUser := make(map[string][]DueTask, 8)
	names := make(map[string]DueTask, 8)
	for _, t := range tasks {
		// A task with no assignee or no address cannot be told to anyone. The
		// query already excludes both; this is the pure function refusing to
		// invent a recipient rather than trusting it.
		if t.AssigneeID == "" || t.AssigneeEmail == "" {
			continue
		}
		if _, seen := byUser[t.AssigneeID]; !seen {
			order = append(order, t.AssigneeID)
			names[t.AssigneeID] = t
		}
		byUser[t.AssigneeID] = append(byUser[t.AssigneeID], t)
	}

	digests := make([]Digest, 0, len(order))
	for _, userID := range order {
		payload, ok := buildPayload(day, byUser[userID], settings)
		if !ok {
			continue
		}
		who := names[userID]
		payload.RecipientName = who.AssigneeName
		digests = append(digests, Digest{
			Recipient: Recipient{UserID: userID, Name: who.AssigneeName, Email: who.AssigneeEmail},
			Payload:   payload,
		})
	}
	return digests
}

// buildPayload buckets one person's tasks. It reports false when nothing landed
// in any bucket — a person whose only tasks fall outside the window gets no
// mail at all, rather than an empty one.
func buildPayload(day TenantDay, tasks []DueTask, settings ReminderSettings) (DigestPayload, bool) {
	var overdue, today, soon []DigestTask
	for _, t := range tasks {
		days := daysBetween(day.LocalDate, t.DueDate)
		line := DigestTask{
			TaskID:       t.TaskID,
			WorkflowID:   t.WorkflowID,
			Name:         t.Name,
			Context:      taskContext(t),
			PeriodCode:   t.PeriodCode,
			DueDate:      t.DueDate,
			DaysUntilDue: days,
		}
		switch {
		case days < 0:
			overdue = append(overdue, line)
		case days == 0:
			today = append(today, line)
		case days <= settings.LeadDays:
			soon = append(soon, line)
		default:
			// Outside the window. The query filters these out; keeping the case
			// explicit means a widened query cannot silently widen the mail.
		}
	}

	payload := DigestPayload{
		LocalDate:  day.LocalDate,
		LeadDays:   settings.LeadDays,
		TotalTasks: len(overdue) + len(today) + len(soon),
		Overdue:    section(overdue, settings.MaxTasksPerBucket),
		DueToday:   section(today, settings.MaxTasksPerBucket),
		DueSoon:    section(soon, settings.MaxTasksPerBucket),
	}
	return payload, payload.TotalTasks > 0
}

// section sorts a bucket by deadline and applies the cap, recording what the
// cap left out. Overdue tasks sort oldest-deadline first, which puts the worst
// arrears at the top — the same order the task board reads.
func section(tasks []DigestTask, max int) DigestSection {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.DueDate != b.DueDate {
			return a.DueDate.Time().Before(b.DueDate.Time())
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.TaskID < b.TaskID
	})
	out := DigestSection{Count: len(tasks)}
	if len(tasks) > max {
		out.Tasks = tasks[:max]
		out.Omitted = len(tasks) - max
		return out
	}
	out.Tasks = tasks
	return out
}

// taskContext is what the line says the work is FOR: the entity, or the
// workflow's own name when there is no entity (a project workflow).
func taskContext(t DueTask) string {
	if t.EntityName != "" {
		return t.EntityName
	}
	return t.WorkflowName
}

// daysBetween counts whole calendar days from `from` to `to`. Both are
// timezone-agnostic legal dates (ADR-0002) whose Time() is midnight UTC, so the
// subtraction is exact — no DST hour can round a deadline into the wrong day,
// which is the bug a "now minus due, divided by 86400" comparison has.
func daysBetween(from, to dateonly.Date) int {
	const day = 24 * 60 * 60
	return int(to.Time().Sub(from.Time()).Seconds()) / day
}

// SummaryFields renders the digest's counts for a log line. Counts only: the
// recipient, the task names and the entity names are all personal or customer
// data and never reach a log (ADR-0015, ADR-0027 decision 9).
//
// Its caller is the reminder job's per-digest debug line (usecases.scanTenant),
// and that is the point of the helper existing rather than the job formatting
// its own string: the one description of a digest that reaches a log is built
// in one place, from fields that are all integers. What this function cannot
// promise is that no OTHER code puts a digest in a log — the rendered body a
// mail adapter handles is a separate path with its own rule.
func (p DigestPayload) SummaryFields() string {
	return "overdue=" + strconv.Itoa(p.Overdue.Count) +
		" today=" + strconv.Itoa(p.DueToday.Count) +
		" soon=" + strconv.Itoa(p.DueSoon.Count)
}
