package middlewares_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
	"github.com/mohamadhallal/zentax-api/logger"
)

// leakedEmail is the address the audit used to reproduce the leak against the
// running stack: GET /members?search=jane.doe%40example.com wrote it verbatim
// into the container log. Every assertion here is written against that exact
// string, so a regression reads as the same finding.
const leakedEmail = "jane.doe@example.com"

// captureLog points the process logger at a buffer for the duration of one test.
//
// None of the tests in this file call t.Parallel(), and that is deliberate:
// logger.Log is a process-wide variable, and Go runs every non-parallel test to
// completion before the parallel batch resumes, so the swap cannot race a
// sibling test that logs.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := logger.Log
	logger.Log = logger.New(&logger.Config{Format: "json", Writer: &buf})
	t.Cleanup(func() { logger.Log = previous })
	return &buf
}

// logLineFor runs one request through a chi router carrying the request logger, so
// the route pattern is resolved exactly as it is in bootstrap.
func logLineFor(t *testing.T, target string, headers http.Header) string {
	t.Helper()

	buf := captureLog(t)

	router := chi.NewRouter()
	router.Use(middlewares.RequestLoggerMiddleware)
	router.Get("/members", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/members/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for key, values := range headers {
		for _, v := range values {
			r.Header.Add(key, v)
		}
	}
	router.ServeHTTP(httptest.NewRecorder(), r)

	line := buf.String()
	require.Contains(t, line, `"msg":"HTTP request"`, "the middleware emitted nothing")
	return line
}

func TestRequestLogger_MemberSearchByEmail_KeepsTheKeyDropsTheTerm(t *testing.T) {
	line := logLineFor(t, "/members?search=jane.doe%40example.com", nil)

	// The finding.
	assert.NotContains(t, line, leakedEmail, "the search term reached the log")
	assert.NotContains(t, line, "jane.doe%40example.com", "the encoded term reached the log")
	assert.NotContains(t, line, "jane", "no part of the term may survive")

	// What replaces it: the shape of the request, none of its content.
	assert.Contains(t, line, `"route":"/members"`)
	assert.Contains(t, line, `"path":"/members"`)
	assert.Contains(t, line, `"query":"search=[redacted]"`,
		"that a search happened is the useful half and must survive")
	assert.Contains(t, line, `"status":200`)
}

func TestRequestLogger_DoesNotLogTheNetworkAddress(t *testing.T) {
	line := logLineFor(t, "/members", nil)

	assert.NotContains(t, line, "remoteAddress")
	assert.NotContains(t, line, "192.0.2.1", "httptest's default RemoteAddr")
}

func TestRequestLogger_DoesNotLogTheRawTarget(t *testing.T) {
	line := logLineFor(t, "/members?search=jane&page=2", nil)

	assert.NotContains(t, line, `"url"`, "the raw request URI field is gone for good")
	assert.Contains(t, line, `"query":"search=[redacted]&page=[redacted]"`)
}

func TestRequestLogger_PathParamsSurvive(t *testing.T) {
	line := logLineFor(t, "/members/0f8fad5b-d9cb-469f-a165-70867728950e", nil)

	assert.Contains(t, line, `"route":"/members/{id}"`)
	assert.Contains(t, line, `"path":"/members/0f8fad5b-d9cb-469f-a165-70867728950e"`,
		"a reference id is exactly what ADR-0015 wants logged")
}

func TestRequestLogger_UnmatchedPathOfFreeText_IsRedacted(t *testing.T) {
	line := logLineFor(t, "/"+leakedEmail, nil)

	assert.NotContains(t, line, leakedEmail)
	assert.Contains(t, line, `"route":"unknown"`)
	assert.Contains(t, line, `"path":"/[redacted]"`)
	assert.Contains(t, line, `"status":404`)
}

func TestRequestLogger_UserAgentIsScrubbedAndBounded(t *testing.T) {
	headers := http.Header{}
	headers.Set("User-Agent", "Mozilla/5.0 (compatible; acme-bot/1.0; +https://acme.test/bot; "+leakedEmail+")")
	line := logLineFor(t, "/members", headers)

	assert.NotContains(t, line, leakedEmail, "crawlers put a contact address in the User-Agent")
	assert.Contains(t, line, "acme-bot/1.0", "the useful half of the header survives")
}
