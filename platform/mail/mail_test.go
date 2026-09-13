package mail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validMessage() Message {
	return Message{
		Kind:    "test",
		From:    Address{Name: "ZenTax", Email: "no-reply@zentax.test"},
		To:      Address{Name: "Jane Doe", Email: "jane@acme.test"},
		Subject: "A subject",
		Text:    "A body.\n",
		HTML:    "<p>A body.</p>\n",
	}
}

// --- Address ----------------------------------------------------------------

func TestAddress_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "jane@acme.test", Address{Email: "jane@acme.test"}.String())
	assert.Equal(t, `"Jane Doe" <jane@acme.test>`, Address{Name: "Jane Doe", Email: "jane@acme.test"}.String())
}

func TestAddress_ValidateRejectsHeaderInjection(t *testing.T) {
	t.Parallel()
	// The reason control characters are refused at the seam: a CR or LF in a
	// display name or a mailbox ends the header it sits in and starts one the
	// attacker chose.
	for _, a := range []Address{
		{Email: "jane@acme.test\r\nBcc: attacker@evil.test"},
		{Name: "Jane\nBcc: attacker@evil.test", Email: "jane@acme.test"},
		{Name: "Jane\r", Email: "jane@acme.test"},
	} {
		require.Error(t, a.Validate(), "%q / %q must be refused", a.Name, a.Email)
	}
}

func TestAddress_ValidateRejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, a := range []Address{
		{},
		{Email: "   "},
		{Email: "not-an-address"},
		{Email: "jane@"},
		{Email: "Jane <jane@acme.test>"}, // a display name belongs in Name
		{Name: strings.Repeat("n", MaxDisplayNameRunes+1), Email: "jane@acme.test"},
	} {
		require.Error(t, a.Validate(), "%q / %q must be refused", a.Name, a.Email)
	}
}

func TestAddress_ValidateAccepts(t *testing.T) {
	t.Parallel()
	for _, a := range []Address{
		{Email: "jane@acme.test"},
		{Name: "Jane Doe", Email: "jane.doe+tax@acme.test"},
		{Name: "Müller GmbH", Email: "buchhaltung@mueller.example"},
		{Email: "ops@localhost"}, // development
	} {
		require.NoError(t, a.Validate(), "%q / %q must be accepted", a.Name, a.Email)
	}
}

func TestAddress_Domain(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "acme.test", Address{Email: "jane@ACME.test"}.Domain())
	assert.Empty(t, Address{Email: "nope"}.Domain())
}

func TestParseAddress(t *testing.T) {
	t.Parallel()
	got, err := ParseAddress(` Jane Doe <jane@acme.test> `)
	require.NoError(t, err)
	assert.Equal(t, Address{Name: "Jane Doe", Email: "jane@acme.test"}, got)

	_, err = ParseAddress("nonsense")
	require.Error(t, err)
}

// --- Message ----------------------------------------------------------------

func TestMessage_ValidateAcceptsAWellFormedMessage(t *testing.T) {
	t.Parallel()
	require.NoError(t, validMessage().Validate())
}

func TestMessage_ValidateRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]func(m *Message){
		"no sender":            func(m *Message) { m.From = Address{} },
		"no recipient":         func(m *Message) { m.To = Address{} },
		"bad reply-to":         func(m *Message) { m.ReplyTo = Address{Email: "nope"} },
		"empty subject":        func(m *Message) { m.Subject = "  " },
		"subject with newline": func(m *Message) { m.Subject = "Reminder\r\nBcc: attacker@evil.test" },
		"no text part":         func(m *Message) { m.Text = "" },
		"control in body":      func(m *Message) { m.Text = "a\x00b" },
		"overlong subject":     func(m *Message) { m.Subject = strings.Repeat("s", MaxSubjectRunes+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := validMessage()
			mutate(&m)
			require.Error(t, m.Validate())
		})
	}
}

func TestMessage_HTMLIsOptional(t *testing.T) {
	t.Parallel()
	m := validMessage()
	m.HTML = ""
	require.NoError(t, m.Validate())
}

// --- Failure classification -------------------------------------------------

func TestClassification_PermanentAndRetryable(t *testing.T) {
	t.Parallel()

	perm := Permanent("smtp: rcpt", "550", errors.New("no such mailbox"))
	require.True(t, IsPermanent(perm))
	require.False(t, IsRetryable(perm))
	assert.True(t, errors.Is(perm, ErrPermanent))
	assert.False(t, errors.Is(perm, ErrRetryable))
	assert.Contains(t, perm.Error(), "550")
	assert.Contains(t, perm.Error(), "permanent")

	temp := Retryable("smtp: dial", "", errors.New("connection refused"))
	require.False(t, IsPermanent(temp))
	require.True(t, IsRetryable(temp))
	assert.True(t, errors.Is(temp, ErrRetryable))
}

func TestClassification_UnwrapReachesTheCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	err := Retryable("smtp: dial", "", cause)
	assert.True(t, errors.Is(err, cause), "the provider error must stay reachable")
}

func TestClassification_UnclassifiedIsRetryable(t *testing.T) {
	t.Parallel()
	// The default that matters: an error from a path nobody classified must
	// cost a retry, not a lost notification.
	assert.True(t, IsRetryable(errors.New("something nobody anticipated")))
	assert.False(t, IsPermanent(errors.New("something nobody anticipated")))
	assert.True(t, IsRetryable(context.DeadlineExceeded))

	// nil is neither.
	assert.False(t, IsRetryable(nil))
	assert.False(t, IsPermanent(nil))
}

func TestClassification_SurvivesWrapping(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(errors.New("context"), Permanent("ses: send-email", "MessageRejected", nil))
	assert.True(t, IsPermanent(wrapped))
}
