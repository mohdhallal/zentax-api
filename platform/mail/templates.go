package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The shipped templates, and the name each one answers to.
//
// The names are a CONTRACT with the transactional outbox: a producer writes a
// row carrying a template name and a frozen JSON payload, and the delivery
// runner asks this package to turn that pair into a message (RenderNamed). The
// same constants are declared on the producer side, in
// modules/notifications/domain, because a platform package must not import a
// module; the JSON keys below mirror that package's payload structs field for
// field, and template_test.go pins them so a rename on either side fails here
// rather than in production as an unrenderable row.
const (
	// TemplateMemberInvited carries InvitePayload: the link that activates an
	// invited member's account.
	TemplateMemberInvited = "member.invited"
	// TemplateDeadlineReminder carries DigestPayload: one person's tax tasks
	// that are overdue, due today, or due within the reminder lead time.
	TemplateDeadlineReminder = "deadline.reminder"
)

// Renderer turns a frozen payload into a message. Each shipped template has
// one; RenderNamed dispatches on the template name.
type Renderer func(b Brand, to Address, payload []byte) (Message, error)

var renderers = map[string]Renderer{
	TemplateMemberInvited:    renderMemberInvitedJSON,
	TemplateDeadlineReminder: renderDeadlineReminderJSON,
}

// TemplateNames lists what this package can render, sorted. A startup check or
// a test can compare it against the producer's vocabulary.
func TemplateNames() []string {
	names := make([]string, 0, len(renderers))
	for name := range renderers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RenderNamed renders the template `name` from a frozen JSON payload — the
// entry point the outbox delivery runner uses.
//
// Every failure is PERMANENT: an unknown template, a payload that will not
// decode and a payload missing a required value are all properties of the row,
// unchanged by waiting. The runner dead-letters the message with the reason
// instead of retrying it to no purpose.
func RenderNamed(b Brand, to Address, name string, payload []byte) (Message, error) {
	render, ok := renderers[name]
	if !ok {
		return Message{}, Permanent("mail: render", name, fmt.Errorf("no template named %q (have %s)", name, strings.Join(TemplateNames(), ", ")))
	}
	return render(b, to, payload)
}

// decodePayload reads a frozen payload. Unknown fields are ALLOWED on purpose:
// a producer that starts sending a new value must not make every message
// already sitting in the queue unrenderable, and the reverse — an old row
// missing a new field — is handled by each renderer's own required-value check.
func decodePayload(name string, payload []byte, into any) error {
	if len(payload) == 0 {
		return Permanent("mail: render", name, errors.New("payload is empty"))
	}
	if err := json.Unmarshal(payload, into); err != nil {
		return Permanent("mail: render", name, fmt.Errorf("payload does not decode: %w", err))
	}
	return nil
}

// withName fills in the recipient's display name from the payload when the
// caller passed a bare mailbox — which is what an outbox row carries.
func withName(to Address, name string) Address {
	if to.Name == "" {
		to.Name = strings.TrimSpace(name)
	}
	return to
}

func greeting(name string) string {
	if n := strings.TrimSpace(name); n != "" {
		return "Hello " + n + ","
	}
	return "Hello,"
}

// --- member.invited ---------------------------------------------------------

// InvitePayload mirrors modules/notifications/domain.InvitePayload.
type InvitePayload struct {
	RecipientName string    `json:"recipientName"`
	InviteToken   string    `json:"inviteToken"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type inviteView struct {
	Greeting  string
	AcceptURL string
	Expires   string
}

func renderMemberInvitedJSON(b Brand, to Address, payload []byte) (Message, error) {
	var data InvitePayload
	if err := decodePayload(TemplateMemberInvited, payload, &data); err != nil {
		return Message{}, err
	}
	return RenderMemberInvited(b, to, data)
}

// RenderMemberInvited builds the invitation message from typed values.
func RenderMemberInvited(b Brand, to Address, d InvitePayload) (Message, error) {
	op := "mail: render " + TemplateMemberInvited
	if strings.TrimSpace(d.InviteToken) == "" {
		return Message{}, Permanent(op, "", errors.New("invite token is required"))
	}
	if d.ExpiresAt.IsZero() {
		return Message{}, Permanent(op, "", errors.New("invite expiry is required"))
	}

	// The accept-invite route and its query parameter are the web app's shape,
	// and knowing it is this layer's job — the producer stores a credential,
	// not a URL.
	acceptURL, err := b.Link("/accept-invite?token=" + url.QueryEscape(d.InviteToken))
	if err != nil {
		return Message{}, Permanent(op, "", err)
	}

	return inviteTemplate.Render(b, withName(to, d.RecipientName), inviteView{
		Greeting:  greeting(d.RecipientName),
		AcceptURL: acceptURL,
		Expires:   formatInstant(d.ExpiresAt),
	})
}

var inviteTemplate = newTemplate[inviteView](
	TemplateMemberInvited,
	`Activate your {{.Product}} account`,
	`{{.Data.Greeting}}

You have been invited to {{.Product}}. Activate your account here:

{{.Data.AcceptURL}}

The invitation expires on {{.Data.Expires}}, and the link works once.
If you were not expecting it, ignore this message: nothing happens until you accept.`,
	`<p style="margin:0 0 16px;font-size:15px;line-height:1.5;">{{.Data.Greeting}}</p>
<p style="margin:0 0 8px;font-size:15px;line-height:1.5;">You have been invited to {{.Product}}. Activate your account here:</p>
<p style="margin:24px 0;">
<a href="{{.Data.AcceptURL}}" style="display:inline-block;background:#4338ca;color:#ffffff;text-decoration:none;padding:11px 20px;border-radius:6px;font-weight:600;">Activate your account</a>
</p>
<p style="margin:0 0 16px;font-size:13px;color:#6b7280;word-break:break-all;">{{.Data.AcceptURL}}</p>
<p style="margin:0;font-size:13px;color:#6b7280;">
The invitation expires on {{.Data.Expires}}, and the link works once.
If you were not expecting it, ignore this message: nothing happens until you accept.
</p>`,
)

// --- deadline.reminder ------------------------------------------------------

// DigestPayload mirrors modules/notifications/domain.DigestPayload: one
// person's tax tasks, bucketed against the tenant's civil day.
type DigestPayload struct {
	RecipientName string        `json:"recipientName"`
	LocalDate     dateonly.Date `json:"localDate"`
	LeadDays      int           `json:"leadDays"`
	TotalTasks    int           `json:"totalTasks"`
	Overdue       DigestSection `json:"overdue"`
	DueToday      DigestSection `json:"dueToday"`
	DueSoon       DigestSection `json:"dueSoon"`
}

// DigestSection is one bucket. Omitted is what the producer's per-bucket cap
// left out, so the mail can say "and N more" without doing arithmetic.
type DigestSection struct {
	Count   int          `json:"count"`
	Omitted int          `json:"omitted"`
	Tasks   []DigestTask `json:"tasks"`
}

// DigestTask is one line.
type DigestTask struct {
	TaskID     string `json:"taskId"`
	WorkflowID string `json:"workflowId"`
	Name       string `json:"name"`
	// Context is the entity the work is for, or the workflow's own name for a
	// project workflow.
	Context      string        `json:"context"`
	PeriodCode   string        `json:"periodCode"`
	DueDate      dateonly.Date `json:"dueDate"`
	DaysUntilDue int           `json:"daysUntilDue"`
}

type digestTaskView struct {
	Context string
	Name    string
	Period  string
	DueDate string
	Note    string
}

type digestSectionView struct {
	Title   string
	Count   int
	Omitted int
	Tasks   []digestTaskView
}

type digestView struct {
	Greeting  string
	LocalDate string
	Summary   string
	Sections  []digestSectionView
	URL       string
}

func renderDeadlineReminderJSON(b Brand, to Address, payload []byte) (Message, error) {
	var data DigestPayload
	if err := decodePayload(TemplateDeadlineReminder, payload, &data); err != nil {
		return Message{}, err
	}
	return RenderDeadlineReminder(b, to, data)
}

// RenderDeadlineReminder builds the deadline digest from typed values.
//
// An empty digest is refused. A message that says "nothing is due" trains its
// reader to skim past the next one, which is the one that mattered — and the
// producer already declines to build one, so reaching here empty is a bug worth
// surfacing rather than mailing.
func RenderDeadlineReminder(b Brand, to Address, d DigestPayload) (Message, error) {
	op := "mail: render " + TemplateDeadlineReminder
	if d.LocalDate.IsZero() {
		return Message{}, Permanent(op, "", errors.New("local date is required"))
	}
	sections := digestSections(d)
	if len(sections) == 0 {
		return Message{}, Permanent(op, "", errors.New("digest has nothing to report"))
	}

	link, err := b.Link("/tasks")
	if err != nil {
		return Message{}, Permanent(op, "", err)
	}

	return reminderTemplate.Render(b, withName(to, d.RecipientName), digestView{
		Greeting:  greeting(d.RecipientName),
		LocalDate: d.LocalDate.String(),
		Summary:   digestSummary(d),
		Sections:  sections,
		URL:       link,
	})
}

// digestSections renders the three buckets in the order a reader needs them —
// what is already late, what is late tonight, what is coming — dropping the
// empty ones so the mail never prints a heading with nothing under it.
func digestSections(d DigestPayload) []digestSectionView {
	buckets := []struct {
		title   string
		section DigestSection
	}{
		{"Overdue", d.Overdue},
		{"Due today", d.DueToday},
		{leadTitle(d.LeadDays), d.DueSoon},
	}

	views := make([]digestSectionView, 0, len(buckets))
	for _, bucket := range buckets {
		if bucket.section.Count == 0 {
			continue
		}
		tasks := make([]digestTaskView, 0, len(bucket.section.Tasks))
		for _, t := range bucket.section.Tasks {
			tasks = append(tasks, digestTaskView{
				Context: strings.TrimSpace(t.Context),
				Name:    strings.TrimSpace(t.Name),
				Period:  strings.TrimSpace(t.PeriodCode),
				DueDate: t.DueDate.String(),
				Note:    taskNote(t.DaysUntilDue),
			})
		}
		views = append(views, digestSectionView{
			Title:   bucket.title,
			Count:   bucket.section.Count,
			Omitted: bucket.section.Omitted,
			Tasks:   tasks,
		})
	}
	return views
}

func leadTitle(leadDays int) string {
	if leadDays == 1 {
		return "Due tomorrow"
	}
	if leadDays <= 0 {
		return "Due soon"
	}
	return "Due within " + strconv.Itoa(leadDays) + " days"
}

// taskNote is the lateness a line carries, derived from the signed day count so
// that the words and the date can never disagree. Due today and due soon say
// nothing extra: the heading already said it.
func taskNote(daysUntilDue int) string {
	switch {
	case daysUntilDue < -1:
		return "overdue by " + strconv.Itoa(-daysUntilDue) + " days"
	case daysUntilDue == -1:
		return "overdue by one day"
	default:
		return ""
	}
}

func digestSummary(d DigestPayload) string {
	var parts []string
	if d.Overdue.Count > 0 {
		parts = append(parts, strconv.Itoa(d.Overdue.Count)+" overdue")
	}
	if d.DueToday.Count > 0 {
		parts = append(parts, strconv.Itoa(d.DueToday.Count)+" due today")
	}
	if d.DueSoon.Count > 0 {
		parts = append(parts, strconv.Itoa(d.DueSoon.Count)+" due soon")
	}
	return strings.Join(parts, ", ")
}

// The digest renders its buckets by ranging over one list, so the text and the
// HTML cannot drift into describing different sections.
//
// There is deliberately ONE link. Per-task deep links would double the length
// of the plain-text part and the web app has no per-task route to point at
// today (/tasks is the board); the payload carries the ids, so adding them is a
// template change on the day the route exists.
var reminderTemplate = newTemplate[digestView](
	TemplateDeadlineReminder,
	`{{.Product}} deadlines for {{.Data.LocalDate}}: {{.Data.Summary}}`,
	`{{.Data.Greeting}}

Your tax deadlines as of {{.Data.LocalDate}}: {{.Data.Summary}}.
{{range .Data.Sections}}
{{.Title}} ({{.Count}}):
{{range .Tasks}}- {{.Context}}: {{.Name}}{{if .Period}} ({{.Period}}){{end}}, due {{.DueDate}}{{if .Note}} — {{.Note}}{{end}}
{{end}}{{if .Omitted}}and {{.Omitted}} more.
{{end}}{{end}}
Open your tasks:
{{.Data.URL}}`,
	`<p style="margin:0 0 16px;font-size:15px;line-height:1.5;">{{.Data.Greeting}}</p>
<p style="margin:0 0 8px;font-size:15px;line-height:1.5;">
Your tax deadlines as of {{.Data.LocalDate}}: <strong>{{.Data.Summary}}</strong>.
</p>
{{range .Data.Sections}}
<p style="margin:22px 0 6px;font-size:14px;font-weight:600;">{{.Title}} ({{.Count}})</p>
<ul style="margin:0;padding-left:20px;font-size:15px;line-height:1.6;">
{{range .Tasks}}<li>{{.Context}}: {{.Name}}{{if .Period}} ({{.Period}}){{end}}, due {{.DueDate}}{{if .Note}} — {{.Note}}{{end}}</li>
{{end}}</ul>
{{if .Omitted}}<p style="margin:6px 0 0;font-size:13px;color:#6b7280;">and {{.Omitted}} more.</p>{{end}}
{{end}}
<p style="margin:24px 0;">
<a href="{{.Data.URL}}" style="display:inline-block;background:#4338ca;color:#ffffff;text-decoration:none;padding:11px 20px;border-radius:6px;font-weight:600;">Open your tasks</a>
</p>
<p style="margin:0;font-size:13px;color:#6b7280;word-break:break-all;">{{.Data.URL}}</p>`,
)

// formatInstant renders a timestamp in UTC, spelled out. Rendering it in the
// recipient's own zone would need a zone the mailer does not have; ADR-0003
// keeps timestamps in UTC, and an explicit "UTC" is honest where a guess would
// not be.
func formatInstant(t time.Time) string {
	return t.UTC().Format("2 January 2006 15:04") + " UTC"
}
