package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInvariant_True_NoPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		Invariant(true, "should not panic")
	})
}

func TestInvariant_False_Panics(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "Invariant violation: condition failed", func() {
		Invariant(false, "condition failed")
	})
}

func TestAssertDefined_NonNil_ReturnsValue(t *testing.T) {
	t.Parallel()

	s := "hello"
	result := AssertDefined(&s, "must not be nil")
	assert.Equal(t, "hello", result)
}

func TestAssertDefined_Nil_Panics(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "Assertion failed: pointer was nil", func() {
		var p *string
		AssertDefined(p, "pointer was nil")
	})
}

func TestAssertDefined_Int_ReturnsValue(t *testing.T) {
	t.Parallel()

	n := 42
	result := AssertDefined(&n, "must not be nil")
	assert.Equal(t, 42, result)
}
