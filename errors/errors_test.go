package apperrors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotFoundError(t *testing.T) {
	t.Parallel()

	err := NewNotFound("resource not found")

	assert.Equal(t, "resource not found", err.Error())
	assert.True(t, err.IsNotFound())
	assert.NotEmpty(t, err.StackTrace())
}

func TestConflictError(t *testing.T) {
	t.Parallel()

	err := NewConflict("already exists")

	assert.Equal(t, "already exists", err.Error())
	assert.True(t, err.IsConflict())
	assert.NotEmpty(t, err.StackTrace())
}

func TestValidationError(t *testing.T) {
	t.Parallel()

	err := NewValidation("invalid input")

	assert.Equal(t, "invalid input", err.Error())
	assert.True(t, err.IsValidation())
	assert.NotEmpty(t, err.StackTrace())
}

func TestUnauthorizedError(t *testing.T) {
	t.Parallel()

	err := NewUnauthorized("not authenticated")

	assert.Equal(t, "not authenticated", err.Error())
	assert.True(t, err.IsUnauthorized())
	assert.NotEmpty(t, err.StackTrace())
}

func TestForbiddenError(t *testing.T) {
	t.Parallel()

	err := NewForbidden("access denied")

	assert.Equal(t, "access denied", err.Error())
	assert.True(t, err.IsForbidden())
	assert.NotEmpty(t, err.StackTrace())
}
