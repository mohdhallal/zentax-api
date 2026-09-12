package httperr

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/logger"
)

// stackErr implements stackTracer to exercise the custom stack path.
type stackError struct{ msg string }

func (e *stackError) Error() string      { return e.msg }
func (e *stackError) StackTrace() string { return "goroutine 1\n\tsome/file.go:42" }

func TestHandleError_DomainError_WritesClassifiedResponse(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/users/123", http.NoBody)

	err := apperrors.NewNotFound("user not found")
	HandleError(w, r, err)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"NOT_FOUND"`)
	assert.Contains(t, w.Body.String(), `"status":false`)
}

func TestHandleError_ConflictError_Returns409(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/users", http.NoBody)

	HandleError(w, r, apperrors.NewConflict("email already exists"))

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"CONFLICT"`)
}

func TestHandleError_ValidationError_Returns400(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/users", http.NoBody)

	HandleError(w, r, apperrors.NewValidation("email: is required"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"VALIDATION"`)
}

func TestHandleError_UnknownError_Returns500(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal", http.NoBody)

	HandleError(w, r, errors.New("db connection lost"))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"INTERNAL"`)
}

func TestHandleError_InternalError_WithStackTracer_UsesProvidedStack(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	// stackErr implements stackTracer — hits the custom stack branch.
	err := &stackError{msg: "something exploded"}
	HandleError(w, r, err)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"INTERNAL"`)
}

func TestHandleError_AppErrorWithDetails_IncludesDetails(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)

	appErr := New(ErrValidation, "bad input", map[string]string{"field": "email"})
	HandleError(w, r, appErr)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "email")
}

// ADR-0015: the response may carry the caller's own data back to them; the log
// may not, because a log line cannot be erased later.
//
// leakedEmail is the address the audit reproduced the leak with. captureLog
// swaps the process logger for a buffer; the tests below do not call
// t.Parallel() for that reason — Go finishes every non-parallel test before the
// parallel batch resumes, so the swap cannot race a sibling that logs.
const leakedEmail = "jane.doe@example.com"

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := logger.Log
	logger.Log = logger.New(&logger.Config{Format: "json", Writer: &buf})
	t.Cleanup(func() { logger.Log = previous })
	return &buf
}

func TestHandleError_DetailsNeverReachTheLog(t *testing.T) {
	buf := captureLog(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/members?search=jane", http.NoBody)

	// The shape build_route.go produces: our own code in the message, the
	// validator's text — which repeats what the caller sent — in the details.
	HandleError(w, r, New(ErrValidation, "INVALID_QUERY", "search: "+leakedEmail+" is not a valid filter"))

	line := buf.String()
	require.Contains(t, line, `"msg":"Request error"`)
	assert.NotContains(t, line, leakedEmail, "the details blob was dumped into the log")
	assert.NotContains(t, line, `"details"`, "the blob itself must not be a field")
	assert.Contains(t, line, `"detailsType":"string"`, "its shape is what a reader needs")
	assert.Contains(t, line, `"code":"VALIDATION"`)
	assert.Contains(t, line, `"path":"/members"`, "the query never reaches this line either")

	// The caller still gets the whole message — it is their own input.
	assert.Contains(t, w.Body.String(), leakedEmail)
}

func TestHandleError_MessageNeverReachesTheLog(t *testing.T) {
	buf := captureLog(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/tenant", http.NoBody)

	// A domain error's text is free text built from caller input: Classify puts
	// err.Error() straight into Message ("unknown timezone: <what was sent>").
	HandleError(w, r, apperrors.NewValidation("unknown timezone: "+leakedEmail))

	line := buf.String()
	assert.NotContains(t, line, leakedEmail)
	assert.NotContains(t, line, `"message"`)
	assert.Contains(t, line, `"code":"VALIDATION"`)
	assert.Contains(t, w.Body.String(), leakedEmail, "the caller is told what was wrong with their input")
}

func TestHandleError_InternalError_LogsTheTypeAndStackNotTheErrorText(t *testing.T) {
	buf := captureLog(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/members", http.NoBody)

	// What a Postgres unique violation actually looks like: the Detail names
	// the column AND the value.
	HandleError(w, r, errors.New(
		`ERROR: duplicate key value violates unique constraint "members_email_key" (SQLSTATE 23505), `+
			`Detail: Key (email)=(`+leakedEmail+`) already exists.`))

	line := buf.String()
	require.Contains(t, line, `"msg":"Internal server error"`)
	assert.NotContains(t, line, leakedEmail, "a driver error's Detail carries the row's value")
	assert.Contains(t, line, `"errorType":"*errors.errorString"`, "the type is the safe half")
	assert.Contains(t, line, `"stack"`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleError_UnmatchedPathOfFreeText_IsRedacted(t *testing.T) {
	buf := captureLog(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+leakedEmail, http.NoBody)

	HandleError(w, r, apperrors.NewNotFound("not found"))

	line := buf.String()
	assert.NotContains(t, line, leakedEmail)
	assert.Contains(t, line, `"path":"/[redacted]"`)
}

func TestCaptureStack_ReturnsNonEmpty(t *testing.T) {
	t.Parallel()

	stack := captureStack()
	assert.NotEmpty(t, stack)
	// Must contain at least one file reference.
	assert.Contains(t, stack, ".go:")
}
