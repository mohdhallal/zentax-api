package logger

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The address the audit reproduced the leak with. Every assertion below that
// says "must not appear" is checked against this exact string, so a regression
// reads as the same finding.
const leakedEmail = "jane.doe@example.com"

func TestRedactQuery_KeepsKeysDropsValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"leading question mark", "?search=jane", "search=[redacted]"},
		{"the audit's request", "search=jane.doe%40example.com", "search=[redacted]"},
		{"every value goes, every key stays", "search=jane&page=2&pageSize=50",
			"search=[redacted]&page=[redacted]&pageSize=[redacted]"},
		{"empty value still redacted", "search=", "search=[redacted]"},
		{"array notation survives", "status[]=open", "status[]=[redacted]"},
		{"dotted and dashed keys survive", "filter.due-date=2026-01-01", "filter.due-date=[redacted]"},
		{"a bare token is a value wearing a key's clothes", "jane.doe%40example.com", "[redacted]"},
		{"a key that is not a parameter name goes too", "jane doe=1", "[redacted]=[redacted]"},
		{"empty key", "=x", "[redacted]=[redacted]"},
		{"blank pairs are skipped", "a=1&&b=2", "a=[redacted]&b=[redacted]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RedactQuery(tc.in))
		})
	}
}

func TestRedactQuery_KeyWithEmailInIt_IsRedacted(t *testing.T) {
	t.Parallel()

	// A key is caller-chosen too, so it gets the same character test as a path
	// segment: '@' is not a parameter name.
	got := RedactQuery(leakedEmail + "=1")
	assert.Equal(t, "[redacted]=[redacted]", got)
	assert.NotContains(t, got, "jane")
}

func TestRedactQuery_BoundsKeyLengthAndPairCount(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("k", maxKeyRunes+1)
	assert.Equal(t, "[redacted]=[redacted]", RedactQuery(long+"=1"))

	var b strings.Builder
	for i := 0; i < maxQueryPairs+10; i++ {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString("k=v")
	}
	got := RedactQuery(b.String())
	assert.Equal(t, maxQueryPairs, strings.Count(got, "k=[redacted]"))
	assert.True(t, strings.HasSuffix(got, "&…"), "the line must say it was cut: %s", got)
}

func TestRedactPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "/"},
		{"root", "/", "/"},
		{"a matched route is untouched", "/members", "/members"},
		{"ids survive", "/entities/0f8fad5b-d9cb-469f-a165-70867728950e/obligations",
			"/entities/0f8fad5b-d9cb-469f-a165-70867728950e/obligations"},
		{"slugs and dots survive", "/tenants/acme-demo/report.v2", "/tenants/acme-demo/report.v2"},
		{"an unmatched path of free text does not", "/" + leakedEmail, "/[redacted]"},
		{"percent-encoding does not sneak through", "/jane%40example.com", "/[redacted]"},
		{"only the offending segment goes", "/members/" + leakedEmail + "/roles",
			"/members/[redacted]/roles"},
		{"a query is cut off", "/members?search=jane", "/members"},
		{"a fragment is cut off", "/members#jane", "/members"},
		{"trailing slash kept", "/members/", "/members/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RedactPath(tc.in))
		})
	}
}

func TestRedactPath_BoundsDepthAndSegmentLength(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "/[redacted]", RedactPath("/"+strings.Repeat("s", maxSegmentRunes+1)))

	deep := strings.Repeat("/a", maxPathSegments+5)
	got := RedactPath(deep)
	assert.Equal(t, maxPathSegments, strings.Count(got, "/a"))
	assert.True(t, strings.HasSuffix(got, "/…"), "the line must say it was cut: %s", got)
}

func TestRedactText_ScrubsEmailsAndClips(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "no address here", RedactText("no address here", MaxTextRunes))

	got := RedactText(`duplicate key value violates unique constraint: Key (email)=(`+leakedEmail+`) already exists`, MaxTextRunes)
	assert.NotContains(t, got, leakedEmail)
	assert.Contains(t, got, "[redacted]")
	assert.Contains(t, got, "unique constraint", "the diagnosis must survive the redaction")

	assert.Equal(t, "abc…", RedactText("abcdef", 3))
	assert.Equal(t, "hél…", RedactText("héllo", 3), "clipping counts runes, not bytes")
	assert.Equal(t, "", RedactText("anything", 0))
}

func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()

	sensitive := []string{
		"email", "userEmail", "e_mail", "mail", "password", "Password",
		"passwd", "secret", "clientSecret", "token", "refresh_token",
		"Authorization", "cookie", "apiKey", "credential", "privateKey",
		"bearer", "phone", "search", "searchTerm", "address", "remoteAddress",
		"remoteAddr", "clientIpAddress",
		"firstName", "lastName", "fullName", "userName", "fileName", "name",
		"otp", "totp", "mfaCode", "ssn", "dob", "iban", "taxId", "passport",
		"ip",
	}
	for _, key := range sensitive {
		assert.True(t, IsSensitiveKey(key), "%q must be treated as sensitive", key)
	}

	// The signals that must survive: reference ids, deployment facts, and the
	// two addresses that are infrastructure rather than people (see redact.go).
	operational := []string{
		"requestId", "tenantId", "userId", "sessionId", "documentId", "route",
		"path", "query", "method", "status", "duration", "code", "stack",
		"detailsType", "errorType", "userAgent", "key", "peer", "namespace",
		"hostname", "forwardedHeader", "port", "env", "mode", "reason", "fix",
	}
	for _, key := range operational {
		assert.False(t, IsSensitiveKey(key), "%q must stay loggable", key)
	}
}

// capture builds a logger writing JSON into a buffer, the way the process
// logger writes JSON into the container's stdout.
func capture(t *testing.T) (Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return New(&Config{Format: "json", Writer: &buf}), &buf
}

func TestRedactHandler_SensitiveKeyValueNeverReachesTheWriter(t *testing.T) {
	t.Parallel()

	l, buf := capture(t)
	l.Info("member lookup", String("email", leakedEmail), String("userId", "u-1"))

	out := buf.String()
	assert.NotContains(t, out, leakedEmail)
	assert.Contains(t, out, `"email":"[redacted]"`)
	assert.Contains(t, out, `"userId":"u-1"`, "a reference id is what the line is for")
}

func TestRedactHandler_ScrubsEmailsUnderAnyKey(t *testing.T) {
	t.Parallel()

	l, buf := capture(t)
	l.Warn("import failed", String("reason", "row 4: "+leakedEmail+" is not a member"))

	out := buf.String()
	assert.NotContains(t, out, leakedEmail)
	assert.Contains(t, out, "row 4:")
}

func TestRedactHandler_ReachesIntoGroupsAndWithAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	base := slog.New(redactHandler{inner: slog.NewJSONHandler(&buf, nil)}).
		With(slog.String("email", leakedEmail))
	base.Info("grouped", slog.Group("member", slog.String("emailAddress", leakedEmail), slog.String("id", "m-1")))

	out := buf.String()
	assert.NotContains(t, out, leakedEmail)
	assert.Contains(t, out, `"id":"m-1"`)
}

type valuer struct{ v string }

func (s valuer) LogValue() slog.Value { return slog.StringValue(s.v) }

func TestRedactHandler_ResolvesLogValuerBeforeJudgingIt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	slog.New(redactHandler{inner: slog.NewJSONHandler(&buf, nil)}).
		Info("lazy", slog.Any("email", valuer{v: leakedEmail}))

	assert.NotContains(t, buf.String(), leakedEmail)
}

func TestError_RendersAsScrubbedText(t *testing.T) {
	t.Parallel()

	l, buf := capture(t)
	l.Error("insert failed", Error(errors.New(`ERROR: duplicate key: Key (email)=(`+leakedEmail+`)`)))

	out := buf.String()
	assert.NotContains(t, out, leakedEmail)
	assert.Contains(t, out, "duplicate key", "the diagnosis must survive")
	assert.Contains(t, out, `"error":"`, "rendered as a string, so the handler can scrub it")

	l2, buf2 := capture(t)
	l2.Error("nil", Error(nil))
	assert.Contains(t, buf2.String(), `"error":""`)
}

func TestNew_DefaultsToStdoutWithoutPanicking(t *testing.T) {
	t.Parallel()

	require.NotNil(t, New(nil))
	require.NotNil(t, New(&Config{Level: "debug", Format: "text"}))
}
