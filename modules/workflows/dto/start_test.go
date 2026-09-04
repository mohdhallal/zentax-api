package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
)

func strp(s string) *string { return &s }

// A cleared date input reaches the API as "" — that is "no override", exactly
// like an absent field; a malformed date is still a 400 naming the key.
func TestStartWorkflowBody_ToDomain_EmptyIsNoOverride(t *testing.T) {
	t.Parallel()
	body := StartWorkflowBody{TaskOverrides: map[string]TaskOverrideBody{
		"tpl_M1": {DueDate: strp(""), PeriodEndDate: strp("2025-01-31"), PaymentDeadline: strp("")},
	}}
	got, err := body.ToDomain()
	require.NoError(t, err)
	ov := got["tpl_M1"]
	assert.Nil(t, ov.DueDate)
	assert.Nil(t, ov.PaymentDeadline)
	require.NotNil(t, ov.PeriodEndDate)
	assert.Equal(t, "2025-01-31", ov.PeriodEndDate.String())

	_, err = StartWorkflowBody{TaskOverrides: map[string]TaskOverrideBody{
		"tpl_M1": {PaymentDeadline: strp("2025-13-01")},
	}}.ToDomain()
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), "invalid paymentDeadline")
}
