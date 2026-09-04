package deadline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func TestFiscalYearStartMonth(t *testing.T) {
	t.Parallel()
	m, err := FiscalYearStartMonth("")
	require.NoError(t, err)
	assert.Equal(t, 1, m)

	m, err = FiscalYearStartMonth("12-31")
	require.NoError(t, err)
	assert.Equal(t, 1, m)

	m, err = FiscalYearStartMonth("03-31")
	require.NoError(t, err)
	assert.Equal(t, 4, m)

	_, err = FiscalYearStartMonth("13-01")
	assert.Error(t, err)
}

func TestPeriodEndDate_CalendarYear(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		code, periodicity string
		want              dateonly.Date
	}{
		"M1":  {"M1", "monthly", dateonly.New(2025, 1, 31)},
		"M2":  {"M2", "monthly", dateonly.New(2025, 2, 28)},
		"Q1":  {"Q1", "quarterly", dateonly.New(2025, 3, 31)},
		"Q4":  {"Q4", "quarterly", dateonly.New(2025, 12, 31)},
		"H1":  {"H1", "bi-annual", dateonly.New(2025, 6, 30)},
		"Y1":  {"Y1", "annual", dateonly.New(2025, 12, 31)},
		"CY1": {"CY1", "consolidated-annual", dateonly.New(2025, 12, 31)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := PeriodEndDate(c.code, c.periodicity, 1, 2025)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestPeriodEndDate_AprMarFiscal(t *testing.T) {
	t.Parallel()
	// FY end March (start month 4); FY2025 = Apr 2024 - Mar 2025.
	end, err := PeriodEndDate("M1", "monthly", 4, 2025)
	require.NoError(t, err)
	assert.Equal(t, dateonly.New(2024, 4, 30), end)

	end, err = PeriodEndDate("M12", "monthly", 4, 2025)
	require.NoError(t, err)
	assert.Equal(t, dateonly.New(2025, 3, 31), end)

	end, err = PeriodEndDate("Q1", "quarterly", 4, 2025)
	require.NoError(t, err)
	assert.Equal(t, dateonly.New(2024, 6, 30), end)
}

func TestPeriodEndDate_Errors(t *testing.T) {
	t.Parallel()
	_, err := PeriodEndDate("M13", "monthly", 1, 2025)
	assert.Error(t, err)
	_, err = PeriodEndDate("X1", "weekly", 1, 2025)
	assert.Error(t, err)
}

func TestApplyOffset(t *testing.T) {
	t.Parallel()
	base := dateonly.New(2025, 1, 31)
	assert.Equal(t, dateonly.New(2025, 2, 10), ApplyOffset(base, 10, "days", "after"))
	assert.Equal(t, dateonly.New(2025, 1, 21), ApplyOffset(base, 10, "days", "before"))
	assert.Equal(t, dateonly.New(2025, 2, 14), ApplyOffset(base, 2, "weeks", "after"))
	// month clamp: Jan 31 + 1mo -> Feb 28
	assert.Equal(t, dateonly.New(2025, 2, 28), ApplyOffset(base, 1, "months", "after"))
	// month clamp: Mar 31 + 1mo -> Apr 30
	assert.Equal(t, dateonly.New(2025, 4, 30), ApplyOffset(dateonly.New(2025, 3, 31), 1, "months", "after"))
}

func TestApplyWeekendAdjustment(t *testing.T) {
	t.Parallel()
	// 2025-03-01 Sat, 2025-03-02 Sun, 2025-02-28 Fri, 2025-03-03 Mon.
	sat := dateonly.New(2025, 3, 1)
	sun := dateonly.New(2025, 3, 2)
	assert.Equal(t, dateonly.New(2025, 3, 3), ApplyWeekendAdjustment(sat, "next-business-day"))
	assert.Equal(t, dateonly.New(2025, 2, 28), ApplyWeekendAdjustment(sat, "prev-business-day"))
	assert.Equal(t, dateonly.New(2025, 3, 3), ApplyWeekendAdjustment(sun, "next-business-day"))
	assert.Equal(t, dateonly.New(2025, 2, 28), ApplyWeekendAdjustment(sun, "prev-business-day"))

	wed := dateonly.New(2025, 3, 5)
	assert.Equal(t, wed, ApplyWeekendAdjustment(wed, "next-business-day"))
	assert.Equal(t, sat, ApplyWeekendAdjustment(sat, "none"))
}

func TestIsSupportedPattern(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "standard", "445", "454", "544", "13-period", "weekly", "custom"} {
		assert.True(t, IsSupportedPattern(p), p)
	}
	assert.False(t, IsSupportedPattern("lunar"))
}
