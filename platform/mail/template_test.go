package mail

import (
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func testBrand(t *testing.T) Brand {
	t.Helper()
	b, err := NewBrand("ZenTax", "https://app.zentax.test/",
		Address{Name: "ZenTax", Email: "no-reply@zentax.test"},
		Address{Email: "support@zentax.test"})
	require.NoError(t, err)
	return b
}

var recipient = Address{Email: "jane@acme.test"}

func sampleInvite() InvitePayload {
	return InvitePayload{
		RecipientName: "Jane Doe",
		InviteToken:   "zti_ab+c/d=",
		ExpiresAt:     time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC),
	}
}

func sampleDigest() DigestPayload {
	return DigestPayload{
		RecipientName: "Jane Doe",
		LocalDate:     dateonly.New(2026, 9, 13),
		LeadDays:      7,
		TotalTasks:    23,
		Overdue: DigestSection{
			Count: 22, Omitted: 20,
			Tasks: []DigestTask{
				{TaskID: "t1", WorkflowID: "w1", Name: "Submit VAT return", Context: "Acme GmbH", PeriodCode: "2026-Q2", DueDate: dateonly.New(2026, 8, 31), DaysUntilDue: -13},
				{TaskID: "t2", WorkflowID: "w2", Name: "Approve CIT computation", Context: "Acme Holding", DueDate: dateonly.New(2026, 9, 12), DaysUntilDue: -1},
			},
		},
		DueToday: DigestSection{
			Count: 1,
			Tasks: []DigestTask{
				{TaskID: "t3", WorkflowID: "w3", Name: "File WHT return", Context: "Acme France", PeriodCode: "2026-08", DueDate: dateonly.New(2026, 9, 13)},
			},
		},
	}
}

// --- Brand ------------------------------------------------------------------

func TestNewBrand_NormalisesAndValidates(t *testing.T) {
	t.Parallel()
	b := testBrand(t)
	assert.Equal(t, "https://app.zentax.test", b.BaseURL, "the trailing slash is stripped so links never double up")

	_, err := NewBrand("", "https://x.test", Address{Email: "a@b.test"}, Address{})
	require.Error(t, err, "a product name is required")

	_, err = NewBrand("ZenTax", "https://x.test", Address{Email: "not-an-address"}, Address{})
	require.Error(t, err, "the sender must be a real mailbox")

	_, err = NewBrand("ZenTax", "https://x.test", Address{Email: "a@b.test"}, Address{Email: "bad"})
	require.Error(t, err, "a reply-to, when set, must be a real mailbox")
}

func TestBrand_LinkWithoutABaseAddressIsAPermanentFailure(t *testing.T) {
	t.Parallel()
	// A cell with no public hostname yet (ADR-0025) can still boot; what it
	// cannot do is send a mail whose only call to action is a broken link.
	b, err := NewBrand("ZenTax", "", Address{Email: "no-reply@zentax.test"}, Address{})
	require.NoError(t, err)

	_, err = RenderMemberInvited(b, recipient, sampleInvite())
	require.Error(t, err)
	assert.True(t, IsPermanent(err), "no retry can conjure a public address")
	assert.Contains(t, err.Error(), "PUBLIC_BASE_URL")
}

// --- member.invited ---------------------------------------------------------

func TestRenderMemberInvited(t *testing.T) {
	t.Parallel()

	msg, err := RenderMemberInvited(testBrand(t), recipient, sampleInvite())
	require.NoError(t, err)

	assert.Equal(t, TemplateMemberInvited, msg.Kind)
	assert.Equal(t, "no-reply@zentax.test", msg.From.Email)
	assert.Equal(t, "support@zentax.test", msg.ReplyTo.Email)
	assert.Equal(t, "jane@acme.test", msg.To.Email)
	assert.Equal(t, "Jane Doe", msg.To.Name, "a bare mailbox picks up the display name from the payload")
	assert.Equal(t, "Activate your ZenTax account", msg.Subject)

	// The link shape is the web app's: the same one the SPA builds itself.
	const want = "https://app.zentax.test/accept-invite?token=zti_ab%2Bc%2Fd%3D"
	assert.Contains(t, msg.Text, want)
	assert.Contains(t, msg.HTML, want)
	assert.Contains(t, msg.Text, "20 September 2026 09:00 UTC")
	assert.Contains(t, msg.Text, "Hello Jane Doe,")
	require.NoError(t, msg.Validate())
}

func TestRenderMemberInvited_RequiresATokenAndAnExpiry(t *testing.T) {
	t.Parallel()
	b := testBrand(t)

	noToken := sampleInvite()
	noToken.InviteToken = " "
	_, err := RenderMemberInvited(b, recipient, noToken)
	require.Error(t, err)
	assert.True(t, IsPermanent(err))

	noExpiry := sampleInvite()
	noExpiry.ExpiresAt = time.Time{}
	_, err = RenderMemberInvited(b, recipient, noExpiry)
	require.Error(t, err)
	assert.True(t, IsPermanent(err))
}

func TestRenderMemberInvited_AnonymousGreeting(t *testing.T) {
	t.Parallel()
	data := sampleInvite()
	data.RecipientName = ""
	msg, err := RenderMemberInvited(testBrand(t), recipient, data)
	require.NoError(t, err)
	assert.Contains(t, msg.Text, "Hello,")
	assert.Empty(t, msg.To.Name)
}

// --- deadline.reminder ------------------------------------------------------

func TestRenderDeadlineReminder(t *testing.T) {
	t.Parallel()

	msg, err := RenderDeadlineReminder(testBrand(t), recipient, sampleDigest())
	require.NoError(t, err)

	assert.Equal(t, TemplateDeadlineReminder, msg.Kind)
	assert.Equal(t, "ZenTax deadlines for 2026-09-13: 22 overdue, 1 due today", msg.Subject)

	assert.Contains(t, msg.Text, "Overdue (22)")
	assert.Contains(t, msg.Text, "Acme GmbH: Submit VAT return (2026-Q2), due 2026-08-31 — overdue by 13 days")
	assert.Contains(t, msg.Text, "overdue by one day", "a single day is not \"1 days\"")
	assert.Contains(t, msg.Text, "and 20 more.", "the producer's per-bucket cap is reported, not hidden")
	assert.Contains(t, msg.Text, "Due today (1)")
	assert.Contains(t, msg.Text, "Acme France: File WHT return (2026-08), due 2026-09-13")
	assert.NotContains(t, msg.Text, "Due within", "an empty bucket prints no heading")
	assert.Contains(t, msg.Text, "https://app.zentax.test/tasks")
	require.NoError(t, msg.Validate())
}

func TestRenderDeadlineReminder_LeadTitleFollowsTheLeadTime(t *testing.T) {
	t.Parallel()

	data := sampleDigest()
	data.DueSoon = DigestSection{Count: 1, Tasks: []DigestTask{
		{Name: "Prepare TP file", Context: "Acme GmbH", DueDate: dateonly.New(2026, 9, 18), DaysUntilDue: 5},
	}}
	msg, err := RenderDeadlineReminder(testBrand(t), recipient, data)
	require.NoError(t, err)
	assert.Contains(t, msg.Text, "Due within 7 days (1)")

	data.LeadDays = 1
	msg, err = RenderDeadlineReminder(testBrand(t), recipient, data)
	require.NoError(t, err)
	assert.Contains(t, msg.Text, "Due tomorrow (1)")
}

func TestRenderDeadlineReminder_RefusesAnEmptyDigest(t *testing.T) {
	t.Parallel()
	_, err := RenderDeadlineReminder(testBrand(t), recipient, DigestPayload{LocalDate: dateonly.New(2026, 9, 13)})
	require.Error(t, err)
	assert.True(t, IsPermanent(err))
	assert.Contains(t, err.Error(), "nothing to report")
}

// --- the name → payload contract with the outbox ----------------------------

func TestTemplateNames(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{TemplateDeadlineReminder, TemplateMemberInvited}, TemplateNames())
	assert.Equal(t, "member.invited", TemplateMemberInvited)
	assert.Equal(t, "deadline.reminder", TemplateDeadlineReminder)
}

// TestRenderNamed_DecodesTheProducersPayload pins the JSON contract: these
// literals are the shape modules/notifications/domain marshals into the outbox
// row. A rename on either side breaks this test rather than a delivery.
func TestRenderNamed_DecodesTheProducersPayload(t *testing.T) {
	t.Parallel()
	b := testBrand(t)

	invite := []byte(`{
		"recipientName": "Jane Doe",
		"inviteToken": "zti_ab+c/d=",
		"expiresAt": "2026-09-20T09:00:00Z"
	}`)
	msg, err := RenderNamed(b, recipient, TemplateMemberInvited, invite)
	require.NoError(t, err)
	assert.Equal(t, TemplateMemberInvited, msg.Kind)
	assert.Contains(t, msg.Text, "accept-invite?token=zti_ab%2Bc%2Fd%3D")
	assert.Contains(t, msg.Text, "20 September 2026 09:00 UTC")

	digest := []byte(`{
		"recipientName": "Jane Doe",
		"localDate": "2026-09-13",
		"leadDays": 7,
		"totalTasks": 2,
		"overdue": {"count": 1, "omitted": 0, "tasks": [
			{"taskId": "t1", "workflowId": "w1", "name": "Submit VAT return",
			 "context": "Acme GmbH", "periodCode": "2026-Q2",
			 "dueDate": "2026-08-31", "daysUntilDue": -13}
		]},
		"dueToday": {"count": 0, "omitted": 0, "tasks": []},
		"dueSoon": {"count": 1, "omitted": 0, "tasks": [
			{"taskId": "t2", "workflowId": "w2", "name": "Prepare TP file",
			 "context": "Acme Holding", "periodCode": "",
			 "dueDate": "2026-09-18", "daysUntilDue": 5}
		]}
	}`)
	msg, err = RenderNamed(b, recipient, TemplateDeadlineReminder, digest)
	require.NoError(t, err)
	assert.Equal(t, TemplateDeadlineReminder, msg.Kind)
	assert.Equal(t, "ZenTax deadlines for 2026-09-13: 1 overdue, 1 due soon", msg.Subject)
	assert.Contains(t, msg.Text, "Acme GmbH: Submit VAT return (2026-Q2), due 2026-08-31 — overdue by 13 days")
	assert.Contains(t, msg.Text, "Acme Holding: Prepare TP file, due 2026-09-18", "a task with no period code prints none")
}

func TestRenderNamed_UnknownTemplateAndBadPayloadArePermanent(t *testing.T) {
	t.Parallel()
	b := testBrand(t)

	_, err := RenderNamed(b, recipient, "nope.invented", []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsPermanent(err), "a row naming a template that does not exist can only be dead-lettered")
	assert.Contains(t, err.Error(), "member.invited")

	_, err = RenderNamed(b, recipient, TemplateMemberInvited, []byte(`{`))
	require.Error(t, err)
	assert.True(t, IsPermanent(err))

	_, err = RenderNamed(b, recipient, TemplateMemberInvited, nil)
	require.Error(t, err)
	assert.True(t, IsPermanent(err))
}

func TestRenderNamed_ToleratesAnUnknownPayloadField(t *testing.T) {
	t.Parallel()
	// A producer that starts sending a new value must not strand every message
	// already in the queue.
	payload := []byte(`{"recipientName":"Jane","inviteToken":"t","expiresAt":"2026-09-20T09:00:00Z","tenantName":"Acme"}`)
	_, err := RenderNamed(testBrand(t), recipient, TemplateMemberInvited, payload)
	require.NoError(t, err)
}

// --- the property that makes multipart honest --------------------------------

// TestTemplates_TextAndHTMLSayTheSameThing is the invariant behind
// multipart/alternative: a recipient whose client shows the plain part must
// learn exactly what a recipient with HTML learns. Every line of the text body
// has to appear in the HTML body's visible text.
func TestTemplates_TextAndHTMLSayTheSameThing(t *testing.T) {
	t.Parallel()

	b := testBrand(t)
	invite, err := RenderMemberInvited(b, recipient, sampleInvite())
	require.NoError(t, err)
	digest, err := RenderDeadlineReminder(b, recipient, sampleDigest())
	require.NoError(t, err)

	for _, msg := range []Message{invite, digest} {
		t.Run(msg.Kind, func(t *testing.T) {
			t.Parallel()
			visible := visibleText(msg.HTML)
			for _, line := range strings.Split(msg.Text, "\n") {
				line = normaliseTextLine(line)
				if len(line) < 3 { // the "--" separator and blank lines
					continue
				}
				assert.Contains(t, visible, line, "the HTML part omits what the text part says")
			}
			assert.Contains(t, visible, msg.From.Name, "the footer carries the product identity")
		})
	}
}

var (
	tagPattern         = regexp.MustCompile(`(?s)<[^>]*>`)
	whitespacePattern  = regexp.MustCompile(`\s+`)
	spaceBeforePunct   = regexp.MustCompile(`\s+([,.;:!?)])`)
	spaceAfterOpenPara = regexp.MustCompile(`\(\s+`)
)

// visibleText is what a reader of the HTML part actually sees: markup out,
// entities decoded, whitespace collapsed the way a renderer collapses it.
func visibleText(htmlBody string) string {
	text := html.UnescapeString(tagPattern.ReplaceAllString(htmlBody, " "))
	text = whitespacePattern.ReplaceAllString(text, " ")
	text = spaceBeforePunct.ReplaceAllString(text, "$1")
	return spaceAfterOpenPara.ReplaceAllString(text, "(")
}

// normaliseTextLine strips the plain-text affordances that have no HTML
// counterpart: a list dash (the HTML uses <li>) and a heading colon (the HTML
// uses weight).
func normaliseTextLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	return strings.TrimSuffix(line, ":")
}
