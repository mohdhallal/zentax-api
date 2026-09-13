package mail

import (
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedOptions() mimeOptions {
	return mimeOptions{
		Now:       time.Date(2026, 9, 13, 8, 30, 0, 0, time.UTC),
		MessageID: "<abc123@zentax.test>",
		Boundary:  "b0undary",
	}
}

// parse reads the built message back with the standard library — the closest
// thing to "what a receiving MTA sees" that a unit test can assert on.
func parse(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	require.NoError(t, err)
	return msg
}

func TestBuildMIME_Headers(t *testing.T) {
	t.Parallel()

	raw, err := buildMIME(validMessage(), fixedOptions())
	require.NoError(t, err)

	parsed := parse(t, raw)
	assert.Equal(t, `"ZenTax" <no-reply@zentax.test>`, parsed.Header.Get("From"))
	assert.Equal(t, `"Jane Doe" <jane@acme.test>`, parsed.Header.Get("To"))
	assert.Equal(t, "A subject", parsed.Header.Get("Subject"))
	assert.Equal(t, "<abc123@zentax.test>", parsed.Header.Get("Message-ID"))
	assert.Equal(t, "auto-generated", parsed.Header.Get("Auto-Submitted"))
	assert.Equal(t, "1.0", parsed.Header.Get("MIME-Version"))

	date, err := parsed.Header.Date()
	require.NoError(t, err)
	assert.True(t, date.Equal(fixedOptions().Now), "got %s", date)

	assert.True(t, strings.Contains(string(raw), "\r\n"), "the wire format is CRLF")
	assert.NotContains(t, string(raw), "Reply-To:", "an unset reply-to writes no header")
}

func TestBuildMIME_ReplyToWhenSet(t *testing.T) {
	t.Parallel()
	m := validMessage()
	m.ReplyTo = Address{Name: "Support", Email: "support@zentax.test"}

	raw, err := buildMIME(m, fixedOptions())
	require.NoError(t, err)
	assert.Equal(t, `"Support" <support@zentax.test>`, parse(t, raw).Header.Get("Reply-To"))
}

func TestBuildMIME_MultipartAlternativeCarriesBothParts(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.Text = "Your VAT return is due on 2026-09-30.\n"
	m.HTML = "<p>Your VAT return is due on <strong>2026-09-30</strong>.</p>\n"

	raw, err := buildMIME(m, fixedOptions())
	require.NoError(t, err)

	parsed := parse(t, raw)
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", mediaType)
	require.Equal(t, "b0undary", params["boundary"])

	reader := multipart.NewReader(parsed.Body, params["boundary"])
	var types, bodies []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		types = append(types, part.Header.Get("Content-Type"))
		decoded, err := io.ReadAll(quotedprintable.NewReader(part))
		require.NoError(t, err)
		bodies = append(bodies, string(decoded))
	}

	require.Len(t, types, 2)
	// Least capable first (RFC 2046 §5.1.4): a text-only client shows the text.
	assert.Equal(t, "text/plain; charset=UTF-8", types[0])
	assert.Equal(t, "text/html; charset=UTF-8", types[1])
	assert.Contains(t, bodies[0], "Your VAT return is due on 2026-09-30.")
	assert.Contains(t, bodies[1], "<strong>2026-09-30</strong>")
}

func TestBuildMIME_TextOnly(t *testing.T) {
	t.Parallel()
	m := validMessage()
	m.HTML = ""

	raw, err := buildMIME(m, fixedOptions())
	require.NoError(t, err)

	parsed := parse(t, raw)
	assert.Equal(t, "text/plain; charset=UTF-8", parsed.Header.Get("Content-Type"))
	assert.Equal(t, "quoted-printable", parsed.Header.Get("Content-Transfer-Encoding"))

	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	require.NoError(t, err)
	// The encoder writes the CRLF line endings the wire wants; the content is
	// otherwise byte-identical.
	assert.Equal(t, m.Text, unwire(string(body)))
}

// unwire turns the CRLF line endings of a decoded part back into the LF the
// template produced, so a body can be compared to its source.
func unwire(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func TestBuildMIME_NonASCIISurvivesBothHeaderAndBody(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.From = Address{Name: "ZenTax Steuern", Email: "no-reply@zentax.test"}
	m.Subject = "Umsatzsteuer-Voranmeldung fällig — Müller GmbH"
	m.Text = "Fällig am 2026-09-30. Zuständig: Müller GmbH.\n"
	m.HTML = "<p>Fällig am 2026-09-30.</p>\n"

	raw, err := buildMIME(m, fixedOptions())
	require.NoError(t, err)

	parsed := parse(t, raw)
	decoded, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, m.Subject, decoded, "the subject round-trips through RFC 2047")

	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	part, err := multipart.NewReader(parsed.Body, params["boundary"]).NextPart()
	require.NoError(t, err)
	body, err := io.ReadAll(quotedprintable.NewReader(part))
	require.NoError(t, err)
	assert.Equal(t, m.Text, unwire(string(body)), "the body round-trips through quoted-printable")
}

func TestBuildMIME_FoldsLongHeaders(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.Subject = strings.TrimSpace(strings.Repeat("a long subject line ", 12))

	raw, err := buildMIME(m, fixedOptions())
	require.NoError(t, err)

	for _, line := range strings.Split(string(raw), "\r\n") {
		assert.LessOrEqual(t, len(line), 998, "no line may exceed the RFC 5322 hard limit: %q", line)
	}
	// Folding must be invisible to a parser.
	assert.Equal(t, m.Subject, parse(t, raw).Header.Get("Subject"))
}

func TestBuildMIME_RejectsAnInvalidMessage(t *testing.T) {
	t.Parallel()
	m := validMessage()
	m.Subject = "Reminder\nBcc: attacker@evil.test"
	_, err := buildMIME(m, fixedOptions())
	require.Error(t, err)
}

func TestBuildMIME_RejectsABodyCarryingTheBoundary(t *testing.T) {
	t.Parallel()
	m := validMessage()
	m.Text = "--b0undary\r\nContent-Type: text/html\r\n\r\ninjected"
	_, err := buildMIME(m, fixedOptions())
	require.Error(t, err)
}

func TestBuildMIME_GeneratesAMessageIDFromTheSenderDomain(t *testing.T) {
	t.Parallel()
	raw, err := BuildMIME(validMessage())
	require.NoError(t, err)

	id := parse(t, raw).Header.Get("Message-ID")
	assert.True(t, strings.HasPrefix(id, "<"), id)
	assert.True(t, strings.HasSuffix(id, "@zentax.test>"), id)

	// Two builds of the same message must not collide.
	other, err := BuildMIME(validMessage())
	require.NoError(t, err)
	assert.NotEqual(t, id, parse(t, other).Header.Get("Message-ID"))
}
