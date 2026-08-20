package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeadlineRule_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()

	original := DeadlineRule{
		Type:              "period_offset",
		Reference:         "period_end",
		OffsetUnit:        "days",
		OffsetValue:       20,
		OffsetDirection:   "after",
		WeekendAdjustment: "next-business-day",
		FixedDates:        []string{"12-31"},
	}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned DeadlineRule
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)
}

func TestDeadlineRule_ScanNilAndEmpty(t *testing.T) {
	t.Parallel()

	var r DeadlineRule
	require.NoError(t, r.Scan(nil))
	assert.Equal(t, DeadlineRule{}, r)

	r2 := DeadlineRule{Type: "fixed"}
	require.NoError(t, r2.Scan([]byte("")))
	assert.Equal(t, DeadlineRule{}, r2)

	var r3 DeadlineRule
	require.NoError(t, r3.Scan([]byte(`{"type":"fixed","fixedDates":["03-31"]}`)))
	assert.Equal(t, "fixed", r3.Type)
	assert.Equal(t, []string{"03-31"}, r3.FixedDates)
}

func TestDeadlineRule_ScanUnsupportedType(t *testing.T) {
	t.Parallel()

	var r DeadlineRule
	assert.Error(t, r.Scan(12345))
}
