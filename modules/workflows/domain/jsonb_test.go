package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDueDateRule_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()

	original := DueDateRule{
		Reference: "period_end", OffsetUnit: "days", OffsetValue: 15,
		OffsetDirection: "after", WeekendAdjustment: "next-business-day",
	}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned DueDateRule
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)
}

func TestPeriods_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()

	original := Periods{"M1", "M2", "Q1"}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned Periods
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)
}

func TestPeriods_NilValueIsEmptyArray(t *testing.T) {
	t.Parallel()

	var p Periods // nil
	v, err := p.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte("[]"), v)
}

func TestJSONB_ScanNilAndData(t *testing.T) {
	t.Parallel()

	var r DueDateRule
	require.NoError(t, r.Scan(nil))
	assert.Equal(t, DueDateRule{}, r)

	var p Periods
	require.NoError(t, p.Scan(nil))
	assert.Equal(t, Periods{}, p)

	require.NoError(t, p.Scan([]byte(`["M3"]`)))
	assert.Equal(t, Periods{"M3"}, p)
}

func TestJSONB_ScanUnsupportedType(t *testing.T) {
	t.Parallel()

	var r DueDateRule
	assert.Error(t, r.Scan(42))

	var p Periods
	assert.Error(t, p.Scan(42))
}
