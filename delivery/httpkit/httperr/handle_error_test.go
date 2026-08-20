package httperr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
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

func TestCaptureStack_ReturnsNonEmpty(t *testing.T) {
	t.Parallel()

	stack := captureStack()
	assert.NotEmpty(t, stack)
	// Must contain at least one file reference.
	assert.Contains(t, stack, ".go:")
}
