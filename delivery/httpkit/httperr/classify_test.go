package httperr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// domain error stubs implementing the error interfaces.

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string    { return e.msg }
func (e *notFoundError) IsNotFound() bool { return true }

type conflictError struct{ msg string }

func (e *conflictError) Error() string    { return e.msg }
func (e *conflictError) IsConflict() bool { return true }

type validationError struct{ msg string }

func (e *validationError) Error() string      { return e.msg }
func (e *validationError) IsValidation() bool { return true }

type forbiddenError struct{ msg string }

func (e *forbiddenError) Error() string     { return e.msg }
func (e *forbiddenError) IsForbidden() bool { return true }

type unauthorizedError struct{ msg string }

func (e *unauthorizedError) Error() string        { return e.msg }
func (e *unauthorizedError) IsUnauthorized() bool { return true }

// Classify tests.

func TestClassify_AppError_Passthrough(t *testing.T) {
	t.Parallel()

	original := New(ErrNotFound, "already an app error")
	result := Classify(original)
	assert.Same(t, original, result)
}

func TestClassify_NotFoundErr(t *testing.T) {
	t.Parallel()

	err := &notFoundError{msg: "record not found"}
	result := Classify(err)
	assert.Equal(t, ErrNotFound, result.Code)
	assert.Equal(t, http.StatusNotFound, result.Status)
	assert.Equal(t, "record not found", result.Message)
}

func TestClassify_ConflictErr(t *testing.T) {
	t.Parallel()

	err := &conflictError{msg: "already exists"}
	result := Classify(err)
	assert.Equal(t, ErrConflict, result.Code)
	assert.Equal(t, http.StatusConflict, result.Status)
}

func TestClassify_ValidationErr(t *testing.T) {
	t.Parallel()

	err := &validationError{msg: "email: invalid email format"}
	result := Classify(err)
	assert.Equal(t, ErrValidation, result.Code)
	assert.Equal(t, http.StatusBadRequest, result.Status)
}

func TestClassify_ForbiddenErr(t *testing.T) {
	t.Parallel()

	err := &forbiddenError{msg: "access denied"}
	result := Classify(err)
	assert.Equal(t, ErrForbidden, result.Code)
	assert.Equal(t, http.StatusForbidden, result.Status)
}

func TestClassify_UnauthorizedErr(t *testing.T) {
	t.Parallel()

	err := &unauthorizedError{msg: "not authenticated"}
	result := Classify(err)
	assert.Equal(t, ErrUnauthorized, result.Code)
	assert.Equal(t, http.StatusUnauthorized, result.Status)
}

func TestClassify_UnknownErr_ReturnsInternal(t *testing.T) {
	t.Parallel()

	err := errors.New("something exploded")
	result := Classify(err)
	assert.Equal(t, ErrInternal, result.Code)
	assert.Equal(t, http.StatusInternalServerError, result.Status)
	assert.Equal(t, "Internal server error", result.Message)
}

// AppError tests.

func TestNew_SetsStatusFromMap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code   AppErrorCode
		status int
	}{
		{ErrValidation, 400},
		{ErrUnauthorized, 401},
		{ErrForbidden, 403},
		{ErrNotFound, 404},
		{ErrConflict, 409},
		{ErrInternal, 500},
	}

	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			t.Parallel()
			e := New(tc.code, "msg")
			assert.Equal(t, tc.status, e.Status)
		})
	}
}

func TestNew_UnknownCode_DefaultsTo500(t *testing.T) {
	t.Parallel()

	e := New("UNKNOWN_CODE", "msg")
	assert.Equal(t, 500, e.Status)
}

func TestNew_WithDetails(t *testing.T) {
	t.Parallel()

	e := New(ErrValidation, "bad input", map[string]string{"field": "email"})
	require.NotNil(t, e.Details)
	details, ok := e.Details.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "email", details["field"])
}

func TestAppError_ErrorString(t *testing.T) {
	t.Parallel()

	e := New(ErrNotFound, "user not found")
	assert.Equal(t, "NOT_FOUND: user not found", e.Error())
}

// Write tests.

func TestWrite_SetsStatusAndJSON(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	Write(w, New(ErrNotFound, "not found"))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"status":false`)
	assert.Contains(t, w.Body.String(), `"NOT_FOUND"`)
	assert.Contains(t, w.Body.String(), `"not found"`)
}

func TestWrite_WithDetails_IncludesInBody(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	Write(w, New(ErrValidation, "bad input", "extra detail"))

	assert.Contains(t, w.Body.String(), `"extra detail"`)
}

// NotFoundHandler tests.

func TestNotFoundHandler_Returns404(t *testing.T) {
	t.Parallel()

	handler := NotFoundHandler()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", http.NoBody)
	w := httptest.NewRecorder()

	handler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "Endpoint not found")
}
