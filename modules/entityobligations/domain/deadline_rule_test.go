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

// TestDeadlineRule_FullBuilderRoundTrip covers the complete legacy builder —
// fixed filing + payment dates, period start, distinct filing/payment offsets,
// and additional deadlines — through Value/Scan, so a JSONB row written by one
// API version reads back identically (ADR-0017: rules are data).
func TestDeadlineRule_FullBuilderRoundTrip(t *testing.T) {
	t.Parallel()

	original := DeadlineRule{
		Type:                "period_offset",
		Reference:           "period_end",
		OffsetUnit:          "days",
		OffsetValue:         20,
		OffsetDirection:     "after",
		WeekendAdjustment:   "prev-business-day",
		FixedDates:          []string{"03-15", "09-15"},
		PaymentFixedDates:   []string{"03-31", "09-30"},
		PeriodStart:         &PeriodStart{Day: 1, Month: 4},
		FilingOffset:        &MonthDayOffset{Months: 1, Days: 20},
		PaymentOffset:       &MonthDayOffset{Months: 2, Days: 0},
		AdditionalDeadlines: []AdditionalDeadline{{Type: "advance_payment", Months: 0, Days: 10}},
	}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned DeadlineRule
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)

	// A nil PaymentOffset ("same as filing") stays nil, not an empty object.
	sameAsFiling := DeadlineRule{Type: "period_offset", FilingOffset: &MonthDayOffset{Days: 20}}
	v2, err := sameAsFiling.Value()
	require.NoError(t, err)
	assert.NotContains(t, string(v2.([]byte)), "paymentOffset")
	var scanned2 DeadlineRule
	require.NoError(t, scanned2.Scan(v2))
	assert.Nil(t, scanned2.PaymentOffset)
	assert.Equal(t, sameAsFiling, scanned2)
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
