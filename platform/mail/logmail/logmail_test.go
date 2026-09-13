package logmail

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/mail"
)

func sender(t *testing.T) (*Sender, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return New(logger.New(&logger.Config{Format: "json", Level: "debug", Writer: &buf})), &buf
}

func message() mail.Message {
	return mail.Message{
		Kind:    "member.invited",
		From:    mail.Address{Name: "ZenTax", Email: "no-reply@zentax.test"},
		To:      mail.Address{Name: "Jane Doe", Email: "jane.doe@acme.test"},
		Subject: "Activate your ZenTax account",
		Text:    "Hello Jane Doe,\n\nhttps://app.zentax.test/accept-invite?token=zti_abc\n",
		HTML:    "<p>Hello Jane Doe,</p>\n",
	}
}

func TestSend_LogsThatAMessageWasRenderedAndNothingFromIt(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	require.NoError(t, s.Send(context.Background(), message()))

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line), "one JSON line: %s", buf.String())

	// What an operator gets: which template, that the recipient was withheld,
	// and how big each part came out.
	assert.Equal(t, "member.invited", line["template"])
	assert.Equal(t, logger.Redacted, line["recipient"])
	assert.Equal(t, float64(len("Activate your ZenTax account")), line["subjectBytes"])
	assert.Equal(t, float64(len(message().Text)), line["textBytes"])
	assert.Equal(t, float64(len("<p>Hello Jane Doe,</p>\n")), line["htmlBytes"])

	// And nothing else: no key carries content.
	for _, key := range []string{"subject", "text", "html", "body", "payload"} {
		assert.NotContains(t, line, key, "the rendered message must not reach the log")
	}
}

// The blocker this adapter shipped with: the whole rendered body in the log,
// which for member.invited is a working credential.
//
// ADR-0027 decision 9 — a delivery "may not log the recipient address, the
// subject, the body, or the rendered template" — is asserted here as TEXT
// rather than as fields, because the property is about the line and not about
// the shape of it: a token that arrives under a different key, or spliced into
// a message string, is the same leak.
func TestSend_NeverWritesACredentialOrABodyToTheLog(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	require.NoError(t, s.Send(context.Background(), message()))
	line := buf.String()

	assert.NotContains(t, line, "zti_", "a live invite credential must never reach a log store")
	assert.NotContains(t, line, "accept-invite", "nor the link built from one")
	assert.NotContains(t, line, "Activate your ZenTax account", "the subject is the tenant's content")
	assert.NotContains(t, line, "Hello Jane Doe", "the greeting names a person")
	assert.NotContains(t, line, "<p>", "no part of the HTML body either")
}

// A digest is the other half of the exposure: no credential, but a tenant's
// entity names, task names and filing dates — customer data with the same
// erasure problem.
func TestSend_NeverWritesATenantsTaskDataToTheLog(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	digest := message()
	digest.Kind = "deadline.reminder"
	digest.Subject = "3 tax tasks need attention"
	digest.Text = "Acme Germany GmbH (DE): Pay prepayment (Q1), due 2026-04-10\n"
	digest.HTML = ""
	require.NoError(t, s.Send(context.Background(), digest))

	line := buf.String()
	assert.Contains(t, line, "deadline.reminder", "the template name is a fixed label and stays")
	assert.NotContains(t, line, "Acme Germany GmbH")
	assert.NotContains(t, line, "Pay prepayment")
	assert.NotContains(t, line, "2026-04-10")
	assert.NotContains(t, line, "3 tax tasks need attention")
}

func TestSend_RedactsTheRecipient(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	require.NoError(t, s.Send(context.Background(), message()))

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))

	assert.Equal(t, logger.Redacted, line["recipient"])
	// ADR-0015: logs sit outside the erasure boundary, so the address must not
	// reach one by ANY field — including inside the rendered body.
	assert.NotContains(t, buf.String(), "jane.doe@acme.test")
	assert.NotContains(t, buf.String(), "acme.test")
}

func TestSend_RejectsAMalformedMessagePermanently(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	m := message()
	m.To = mail.Address{Email: "not-an-address"}

	err := s.Send(context.Background(), m)
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err), "a malformed message never becomes valid by waiting")
	assert.Empty(t, buf.String(), "a message that was refused is not also logged as rendered")
}

func TestSend_RejectsAHeaderInjectingSubject(t *testing.T) {
	t.Parallel()

	s, _ := sender(t)
	m := message()
	m.Subject = "Reminder\r\nBcc: attacker@evil.test"

	err := s.Send(context.Background(), m)
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err))
}

func TestSend_CancelledContextIsRetryable(t *testing.T) {
	t.Parallel()

	s, _ := sender(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Send(ctx, message())
	require.Error(t, err)
	assert.True(t, mail.IsRetryable(err), "a shutdown mid-tick must leave the message queued")
}

func TestNew_AcceptsANilLogger(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { _ = New(nil) })
}

func TestSender_SatisfiesTheSeam(t *testing.T) {
	t.Parallel()
	var s mail.Sender = New(nil)
	assert.NotNil(t, s)
}

func TestSend_OneLinePerMessage(t *testing.T) {
	t.Parallel()

	s, buf := sender(t)
	require.NoError(t, s.Send(context.Background(), message()))
	require.NoError(t, s.Send(context.Background(), message()))

	assert.Len(t, strings.Split(strings.TrimSpace(buf.String()), "\n"), 2)
}
