package deadline

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// d is a compact date literal for the tables below.
func d(y, m, day int) dateonly.Date { return dateonly.New(y, m, day) }

func mustCalendar(t *testing.T, pattern, fyEnd, weekEnd, rule string, custom []CustomPeriod, fy int) *Calendar {
	t.Helper()
	c, err := NewCalendar(pattern, fyEnd, weekEnd, rule, custom, fy)
	require.NoError(t, err)
	return c
}

type want struct {
	code       string
	start, end dateonly.Date
}

func assertPeriods(t *testing.T, got []Period, wants []want) {
	t.Helper()
	require.Len(t, got, len(wants))
	for i, w := range wants {
		assert.Equal(t, w.code, got[i].Code, "period %d code", i)
		assert.Equal(t, w.start, got[i].Start, "period %s start", w.code)
		assert.Equal(t, w.end, got[i].End, "period %s end", w.code)
		assert.NotEmpty(t, got[i].Label)
		// Contiguity: every period starts the day after the previous one ends.
		if i > 0 {
			assert.Equal(t, addDays(got[i-1].End, 1), got[i].Start, "period %s is not contiguous", w.code)
		}
	}
}

// ---- standard ---------------------------------------------------------------

func TestCalendar_StandardCalendarYear(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "standard", "12-31", "", "", nil, 2025)

	monthly, err := c.Periods("monthly")
	require.NoError(t, err)
	assertPeriods(t, monthly, []want{
		{"M1", d(2025, 1, 1), d(2025, 1, 31)}, {"M2", d(2025, 2, 1), d(2025, 2, 28)},
		{"M3", d(2025, 3, 1), d(2025, 3, 31)}, {"M4", d(2025, 4, 1), d(2025, 4, 30)},
		{"M5", d(2025, 5, 1), d(2025, 5, 31)}, {"M6", d(2025, 6, 1), d(2025, 6, 30)},
		{"M7", d(2025, 7, 1), d(2025, 7, 31)}, {"M8", d(2025, 8, 1), d(2025, 8, 31)},
		{"M9", d(2025, 9, 1), d(2025, 9, 30)}, {"M10", d(2025, 10, 1), d(2025, 10, 31)},
		{"M11", d(2025, 11, 1), d(2025, 11, 30)}, {"M12", d(2025, 12, 1), d(2025, 12, 31)},
	})
	assert.Equal(t, "M1 (1 Jan – 31 Jan 2025)", monthly[0].Label)

	quarterly, err := c.Periods("quarterly")
	require.NoError(t, err)
	assertPeriods(t, quarterly, []want{
		{"Q1", d(2025, 1, 1), d(2025, 3, 31)}, {"Q2", d(2025, 4, 1), d(2025, 6, 30)},
		{"Q3", d(2025, 7, 1), d(2025, 9, 30)}, {"Q4", d(2025, 10, 1), d(2025, 12, 31)},
	})
	half, err := c.Periods("bi-annual")
	require.NoError(t, err)
	assertPeriods(t, half, []want{{"H1", d(2025, 1, 1), d(2025, 6, 30)}, {"H2", d(2025, 7, 1), d(2025, 12, 31)}})
	annual, err := c.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, annual, []want{{"Y1", d(2025, 1, 1), d(2025, 12, 31)}})
	cy, err := c.Periods("consolidated-annual")
	require.NoError(t, err)
	assertPeriods(t, cy, []want{{"CY1", d(2025, 1, 1), d(2025, 12, 31)}})

	end, err := c.PeriodEnd("Q2", "quarterly")
	require.NoError(t, err)
	assert.Equal(t, d(2025, 6, 30), end)
	assert.Equal(t, d(2025, 12, 31), c.YearEnd())
}

func TestCalendar_StandardEmptyFYEndIsCalendarYear(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "", "", "", "", nil, 2025)
	end, err := c.PeriodEnd("M12", "monthly")
	require.NoError(t, err)
	assert.Equal(t, d(2025, 12, 31), end)
}

// An April-start year (UK): FY2026 = 1 Apr 2025 – 31 Mar 2026.
func TestCalendar_StandardAprilStart(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "standard", "03-31", "", "", nil, 2026)

	monthly, err := c.Periods("monthly")
	require.NoError(t, err)
	require.Len(t, monthly, 12)
	assert.Equal(t, want{"M1", d(2025, 4, 1), d(2025, 4, 30)}, want{monthly[0].Code, monthly[0].Start, monthly[0].End})
	assert.Equal(t, want{"M9", d(2025, 12, 1), d(2025, 12, 31)}, want{monthly[8].Code, monthly[8].Start, monthly[8].End})
	assert.Equal(t, want{"M10", d(2026, 1, 1), d(2026, 1, 31)}, want{monthly[9].Code, monthly[9].Start, monthly[9].End})
	assert.Equal(t, want{"M12", d(2026, 3, 1), d(2026, 3, 31)}, want{monthly[11].Code, monthly[11].Start, monthly[11].End})

	quarterly, err := c.Periods("quarterly")
	require.NoError(t, err)
	assertPeriods(t, quarterly, []want{
		{"Q1", d(2025, 4, 1), d(2025, 6, 30)}, {"Q2", d(2025, 7, 1), d(2025, 9, 30)},
		{"Q3", d(2025, 10, 1), d(2025, 12, 31)}, {"Q4", d(2026, 1, 1), d(2026, 3, 31)},
	})
	half, err := c.Periods("bi-annual")
	require.NoError(t, err)
	assertPeriods(t, half, []want{{"H1", d(2025, 4, 1), d(2025, 9, 30)}, {"H2", d(2025, 10, 1), d(2026, 3, 31)}})
	annual, err := c.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, annual, []want{{"Y1", d(2025, 4, 1), d(2026, 3, 31)}})
	assert.Equal(t, "Y1 (1 Apr 2025 – 31 Mar 2026)", annual[0].Label)
}

// Standard weekly: seven-day weeks from the fiscal-year start; W52 absorbs the
// remainder (8 days in a common year, 9 in a leap year) and ends on the FY end.
func TestCalendar_StandardWeeklyRemainder(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "standard", "12-31", "", "", nil, 2025)
	weeks, err := c.Periods("weekly")
	require.NoError(t, err)
	require.Len(t, weeks, 52)
	assert.Equal(t, want{"W1", d(2025, 1, 1), d(2025, 1, 7)}, want{weeks[0].Code, weeks[0].Start, weeks[0].End})
	assert.Equal(t, want{"W2", d(2025, 1, 8), d(2025, 1, 14)}, want{weeks[1].Code, weeks[1].Start, weeks[1].End})
	assert.Equal(t, want{"W51", d(2025, 12, 17), d(2025, 12, 23)}, want{weeks[50].Code, weeks[50].Start, weeks[50].End})
	assert.Equal(t, want{"W52", d(2025, 12, 24), d(2025, 12, 31)}, want{weeks[51].Code, weeks[51].Start, weeks[51].End})

	leap := mustCalendar(t, "standard", "12-31", "", "", nil, 2024)
	weeks, err = leap.Periods("weekly")
	require.NoError(t, err)
	assert.Equal(t, want{"W52", d(2024, 12, 23), d(2024, 12, 31)}, want{weeks[51].Code, weeks[51].Start, weeks[51].End})

	// April start: W1 begins 1 Apr, W52 ends 31 Mar.
	uk := mustCalendar(t, "standard", "03-31", "", "", nil, 2026)
	weeks, err = uk.Periods("weekly")
	require.NoError(t, err)
	assert.Equal(t, d(2025, 4, 1), weeks[0].Start)
	assert.Equal(t, d(2026, 3, 31), weeks[51].End)
}

// ---- week-based -------------------------------------------------------------

// NRF fiscal 2023 (Saturday nearest 31 Jan): previous year end 28 Jan 2023,
// year end 3 Feb 2024 → 371 days → 53 weeks; the 53rd week goes to the last
// period. The 4-4-5 months are hand-derived from the 29 Jan 2023 start.
func TestCalendar_445_NRF2023_53Weeks(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "445", "01-31", "saturday", "nearest", nil, 2024)
	assert.Equal(t, 53, c.Weeks())
	assert.Equal(t, d(2024, 2, 3), c.YearEnd())

	monthly, err := c.Periods("monthly")
	require.NoError(t, err)
	assertPeriods(t, monthly, []want{
		{"M1", d(2023, 1, 29), d(2023, 2, 25)},
		{"M2", d(2023, 2, 26), d(2023, 3, 25)},
		{"M3", d(2023, 3, 26), d(2023, 4, 29)},
		{"M4", d(2023, 4, 30), d(2023, 5, 27)},
		{"M5", d(2023, 5, 28), d(2023, 6, 24)},
		{"M6", d(2023, 6, 25), d(2023, 7, 29)},
		{"M7", d(2023, 7, 30), d(2023, 8, 26)},
		{"M8", d(2023, 8, 27), d(2023, 9, 23)},
		{"M9", d(2023, 9, 24), d(2023, 10, 28)},
		{"M10", d(2023, 10, 29), d(2023, 11, 25)},
		{"M11", d(2023, 11, 26), d(2023, 12, 23)},
		{"M12", d(2023, 12, 24), d(2024, 2, 3)}, // 5 + 1 = 6 weeks
	})
	assert.Equal(t, "M12 (24 Dec 2023 – 3 Feb 2024)", monthly[11].Label)

	quarterly, err := c.Periods("quarterly")
	require.NoError(t, err)
	assertPeriods(t, quarterly, []want{
		{"Q1", d(2023, 1, 29), d(2023, 4, 29)},
		{"Q2", d(2023, 4, 30), d(2023, 7, 29)},
		{"Q3", d(2023, 7, 30), d(2023, 10, 28)},
		{"Q4", d(2023, 10, 29), d(2024, 2, 3)}, // 14 weeks
	})
	half, err := c.Periods("bi-annual")
	require.NoError(t, err)
	assertPeriods(t, half, []want{{"H1", d(2023, 1, 29), d(2023, 7, 29)}, {"H2", d(2023, 7, 30), d(2024, 2, 3)}})
	annual, err := c.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, annual, []want{{"Y1", d(2023, 1, 29), d(2024, 2, 3)}})
	cy, err := c.Periods("consolidated-annual")
	require.NoError(t, err)
	assertPeriods(t, cy, []want{{"CY1", d(2023, 1, 29), d(2024, 2, 3)}})

	// Weekly: W53 exists and ends on the year end.
	weeks, err := c.Periods("weekly")
	require.NoError(t, err)
	require.Len(t, weeks, 53)
	assert.Equal(t, want{"W1", d(2023, 1, 29), d(2023, 2, 4)}, want{weeks[0].Code, weeks[0].Start, weeks[0].End})
	assert.Equal(t, want{"W53", d(2024, 1, 28), d(2024, 2, 3)}, want{weeks[52].Code, weeks[52].Start, weeks[52].End})
	for i := 1; i < len(weeks); i++ {
		assert.Equal(t, addDays(weeks[i-1].End, 1), weeks[i].Start)
		assert.Equal(t, 6, daysBetween(weeks[i].Start, weeks[i].End))
	}
}

// NRF fiscal 2024 (same anchor, next year): 3 Feb 2024 → 1 Feb 2025 = 364 days → 52 weeks.
func TestCalendar_445_52Weeks(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "445", "01-31", "saturday", "nearest", nil, 2025)
	assert.Equal(t, 52, c.Weeks())

	monthly, err := c.Periods("monthly")
	require.NoError(t, err)
	require.Len(t, monthly, 12)
	assert.Equal(t, want{"M1", d(2024, 2, 4), d(2024, 3, 2)}, want{monthly[0].Code, monthly[0].Start, monthly[0].End})
	assert.Equal(t, want{"M3", d(2024, 3, 31), d(2024, 5, 4)}, want{monthly[2].Code, monthly[2].Start, monthly[2].End})
	assert.Equal(t, want{"M12", d(2024, 12, 29), d(2025, 2, 1)}, want{monthly[11].Code, monthly[11].Start, monthly[11].End})

	weeks, err := c.Periods("weekly")
	require.NoError(t, err)
	require.Len(t, weeks, 52)
	assert.Equal(t, d(2025, 2, 1), weeks[51].End)

	_, err = c.Period("W53", "weekly")
	assert.Error(t, err, "a 52-week year has no W53")
}

// 'last' vs 'nearest' on a Sunday week end with a 31 Dec anchor:
//   - 31 Dec 2023 IS a Sunday → both rules agree (distance 0).
//   - 31 Dec 2022 is a Saturday → 'last' = 25 Dec 2022 (6 days back), 'nearest'
//     = 1 Jan 2023 (1 day forward). So FY2023 has 53 weeks under 'last' and 52
//     under 'nearest' — the rule changes the year length, not just its end.
func TestCalendar_YearEndRule_LastVsNearest(t *testing.T) {
	t.Parallel()
	last := mustCalendar(t, "445", "12-31", "sunday", "last", nil, 2023)
	nearest := mustCalendar(t, "445", "12-31", "sunday", "nearest", nil, 2023)

	assert.Equal(t, d(2023, 12, 31), last.YearEnd())
	assert.Equal(t, d(2023, 12, 31), nearest.YearEnd())
	assert.Equal(t, 53, last.Weeks())
	assert.Equal(t, 52, nearest.Weeks())

	lp, err := last.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, lp, []want{{"Y1", d(2022, 12, 26), d(2023, 12, 31)}})
	np, err := nearest.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, np, []want{{"Y1", d(2023, 1, 2), d(2023, 12, 31)}})

	// FY2024: 31 Dec 2024 is a Tuesday → both rules pick Sunday 29 Dec 2024.
	last24 := mustCalendar(t, "445", "12-31", "sunday", "last", nil, 2024)
	nearest24 := mustCalendar(t, "445", "12-31", "sunday", "nearest", nil, 2024)
	assert.Equal(t, d(2024, 12, 29), last24.YearEnd())
	assert.Equal(t, d(2024, 12, 29), nearest24.YearEnd())
}

// The 'nearest' boundary: with seven-day weeks the candidates are never
// equidistant; three days back beats four days forward, four days back loses
// to three days forward. Saturday week end, anchor 31 Jan:
//   - 31 Jan 2023 is a Tuesday: 28 Jan (−3) vs 4 Feb (+4) → 28 Jan.
//   - 31 Jan 2024 is a Wednesday: 27 Jan (−4) vs 3 Feb (+3) → 3 Feb.
func TestCalendar_NearestBoundary(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "445", "01-31", "saturday", "nearest", nil, 2024)
	assert.Equal(t, d(2024, 2, 3), c.YearEnd())
	assert.Equal(t, d(2023, 1, 28), addDays(c.yearStart, -1))

	// 'last' on the same anchors: 28 Jan 2023 and 27 Jan 2024 → 364 days.
	l := mustCalendar(t, "445", "01-31", "saturday", "last", nil, 2024)
	assert.Equal(t, d(2024, 1, 27), l.YearEnd())
	assert.Equal(t, 52, l.Weeks())
}

// Every week-end day resolves against a Wednesday 31 Dec 2025 anchor ('last').
func TestCalendar_AllWeekEndDays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		day   string
		end   dateonly.Date
		weeks int
	}{
		{"monday", d(2025, 12, 29), 52},
		{"tuesday", d(2025, 12, 30), 52},
		{"wednesday", d(2025, 12, 31), 53}, // 25 Dec 2024 → 31 Dec 2025 = 371 days
		{"thursday", d(2025, 12, 25), 52},
		{"friday", d(2025, 12, 26), 52},
		{"saturday", d(2025, 12, 27), 52},
		{"sunday", d(2025, 12, 28), 52},
	}
	for _, tc := range cases {
		t.Run(tc.day, func(t *testing.T) {
			t.Parallel()
			c := mustCalendar(t, "445", "12-31", tc.day, "last", nil, 2025)
			assert.Equal(t, tc.end, c.YearEnd())
			assert.Equal(t, tc.weeks, c.Weeks())
			annual, err := c.Periods("annual")
			require.NoError(t, err)
			assert.Equal(t, tc.end, annual[0].End)
			assert.Equal(t, tc.weeks*7-1, daysBetween(annual[0].Start, annual[0].End))
		})
	}
}

// Same 52-week year (Saturday 'last', 31 Dec anchor, FY2025 = 29 Dec 2024 –
// 27 Dec 2025) under the three quarter shapes: the quarter boundaries agree,
// the month boundaries differ.
func TestCalendar_445_454_544_Boundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		m1, m2  dateonly.Date // period ends of M1 and M2
	}{
		{"445", d(2025, 1, 25), d(2025, 2, 22)},
		{"454", d(2025, 1, 25), d(2025, 3, 1)},
		{"544", d(2025, 2, 1), d(2025, 3, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			t.Parallel()
			c := mustCalendar(t, tc.pattern, "12-31", "saturday", "last", nil, 2025)
			assert.Equal(t, 52, c.Weeks())
			monthly, err := c.Periods("monthly")
			require.NoError(t, err)
			require.Len(t, monthly, 12)
			assert.Equal(t, d(2024, 12, 29), monthly[0].Start)
			assert.Equal(t, tc.m1, monthly[0].End)
			assert.Equal(t, tc.m2, monthly[1].End)
			assert.Equal(t, d(2025, 3, 29), monthly[2].End, "Q1 ends on the same day for every shape")
			assert.Equal(t, d(2025, 12, 27), monthly[11].End)

			quarterly, err := c.Periods("quarterly")
			require.NoError(t, err)
			assertPeriods(t, quarterly, []want{
				{"Q1", d(2024, 12, 29), d(2025, 3, 29)},
				{"Q2", d(2025, 3, 30), d(2025, 6, 28)},
				{"Q3", d(2025, 6, 29), d(2025, 9, 27)},
				{"Q4", d(2025, 9, 28), d(2025, 12, 27)},
			})
		})
	}
}

// 13-period on the NRF 2023 anchor: P1..P12 are four weeks each, P13 takes
// the 53rd week (five weeks, 31 Dec 2023 – 3 Feb 2024).
func TestCalendar_13Period_53rdWeekInP13(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "13-period", "01-31", "saturday", "nearest", nil, 2024)
	assert.Equal(t, 53, c.Weeks())

	periods, err := c.Periods("monthly")
	require.NoError(t, err)
	require.Len(t, periods, 13)
	for i := 0; i < 12; i++ {
		assert.Equal(t, "P"+strconv.Itoa(i+1), periods[i].Code)
		assert.Equal(t, 27, daysBetween(periods[i].Start, periods[i].End), "P%d is four weeks", i+1)
	}
	assert.Equal(t, want{"P1", d(2023, 1, 29), d(2023, 2, 25)}, want{periods[0].Code, periods[0].Start, periods[0].End})
	assert.Equal(t, want{"P12", d(2023, 12, 3), d(2023, 12, 30)}, want{periods[11].Code, periods[11].Start, periods[11].End})
	assert.Equal(t, want{"P13", d(2023, 12, 31), d(2024, 2, 3)}, want{periods[12].Code, periods[12].Start, periods[12].End})

	quarterly, err := c.Periods("quarterly")
	require.NoError(t, err)
	assertPeriods(t, quarterly, []want{
		{"Q1", d(2023, 1, 29), d(2023, 4, 29)},
		{"Q2", d(2023, 4, 30), d(2023, 7, 29)},
		{"Q3", d(2023, 7, 30), d(2023, 10, 28)},
		{"Q4", d(2023, 10, 29), d(2024, 2, 3)},
	})

	_, err = c.Period("M1", "monthly")
	assert.Error(t, err, "13-period uses P codes, not M codes")
	_, err = c.Period("P14", "monthly")
	assert.Error(t, err)

	// A 52-week 13-period year: P13 is four weeks like the rest.
	c52 := mustCalendar(t, "13-period", "01-31", "saturday", "nearest", nil, 2025)
	periods, err = c52.Periods("monthly")
	require.NoError(t, err)
	assert.Equal(t, 27, daysBetween(periods[12].Start, periods[12].End))
	assert.Equal(t, d(2025, 2, 1), periods[12].End)
}

// The weekly pattern is a 52/53-week year whose monthly grouping is 4-4-5.
func TestCalendar_WeeklyPattern(t *testing.T) {
	t.Parallel()
	c := mustCalendar(t, "weekly", "01-31", "saturday", "nearest", nil, 2024)
	assert.Equal(t, 53, c.Weeks())

	weeks, err := c.Periods("weekly")
	require.NoError(t, err)
	require.Len(t, weeks, 53)
	assert.Equal(t, d(2024, 2, 3), weeks[52].End)

	monthly, err := c.Periods("monthly")
	require.NoError(t, err)
	require.Len(t, monthly, 12)
	assert.Equal(t, d(2023, 2, 25), monthly[0].End)
	assert.Equal(t, d(2023, 4, 29), monthly[2].End)
	assert.Equal(t, d(2024, 2, 3), monthly[11].End)
}

func TestCalendar_WeekBasedDefaults(t *testing.T) {
	t.Parallel()
	// "" week-end day = Saturday, "" rule = nearest, "" FY end = 31 Dec.
	c := mustCalendar(t, "445", "", "", "", nil, 2025)
	explicit := mustCalendar(t, "445", "12-31", "saturday", "nearest", nil, 2025)
	assert.Equal(t, explicit.YearEnd(), c.YearEnd())
	assert.Equal(t, d(2026, 1, 3), c.YearEnd()) // Wed 31 Dec 2025 → Sat 3 Jan 2026 (+3 beats −4)
}

// ---- custom -----------------------------------------------------------------

func TestCalendar_CustomPeriods(t *testing.T) {
	t.Parallel()
	periods := []CustomPeriod{
		{Code: "T1", Name: "Trimester 1", StartDate: "01-01", EndDate: "04-30"},
		{Code: "T2", Name: "Trimester 2", StartDate: "05-01", EndDate: "08-31"},
		{Code: "T3", Name: "Trimester 3", StartDate: "09-01", EndDate: "12-31"},
	}
	c := mustCalendar(t, "custom", "12-31", "", "", periods, 2026)

	for _, periodicity := range []string{"monthly", "quarterly", "bi-annual"} {
		got, err := c.Periods(periodicity)
		require.NoError(t, err)
		assertPeriods(t, got, []want{
			{"T1", d(2026, 1, 1), d(2026, 4, 30)},
			{"T2", d(2026, 5, 1), d(2026, 8, 31)},
			{"T3", d(2026, 9, 1), d(2026, 12, 31)},
		})
		assert.Equal(t, "Trimester 1 (1 Jan – 30 Apr 2026)", got[0].Label)
	}
	end, err := c.PeriodEnd("T2", "monthly")
	require.NoError(t, err)
	assert.Equal(t, d(2026, 8, 31), end)

	annual, err := c.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, annual, []want{{"Y1", d(2026, 1, 1), d(2026, 12, 31)}})
	cy, err := c.Periods("consolidated-annual")
	require.NoError(t, err)
	assert.Equal(t, "CY1", cy[0].Code)

	_, err = c.Period("M1", "monthly")
	assert.ErrorContains(t, err, `"M1"`, "only the listed codes are valid")
}

// A custom period that straddles the calendar-year boundary: under a
// January-start year every end date lands in the fiscal year, so a 15 Dec
// start placed in the same year is AFTER its 14 Jan end and moves back a year.
func TestCalendar_CustomPeriodsStraddleYearBoundary(t *testing.T) {
	t.Parallel()
	periods := []CustomPeriod{
		{Code: "S1", Name: "Winter", StartDate: "12-15", EndDate: "01-14"},
		{Code: "S2", Name: "Rest", StartDate: "01-15", EndDate: "12-14"},
	}
	c := mustCalendar(t, "custom", "12-31", "", "", periods, 2026)
	got, err := c.Periods("monthly")
	require.NoError(t, err)
	assertPeriods(t, got, []want{
		{"S1", d(2025, 12, 15), d(2026, 1, 14)},
		{"S2", d(2026, 1, 15), d(2026, 12, 14)},
	})
	assert.Equal(t, "Winter (15 Dec 2025 – 14 Jan 2026)", got[0].Label)
}

// Custom periods under an April-start year use the standard placement rule:
// months before April belong to the year the fiscal year ends in.
func TestCalendar_CustomPeriodsAprilStart(t *testing.T) {
	t.Parallel()
	periods := []CustomPeriod{
		{Code: "A", Name: "Apr–Nov", StartDate: "04-01", EndDate: "11-30"},
		{Code: "B", Name: "Dec–Mar", StartDate: "12-01", EndDate: "03-31"},
	}
	c := mustCalendar(t, "custom", "03-31", "", "", periods, 2026)
	got, err := c.Periods("quarterly")
	require.NoError(t, err)
	assertPeriods(t, got, []want{
		{"A", d(2025, 4, 1), d(2025, 11, 30)},
		{"B", d(2025, 12, 1), d(2026, 3, 31)},
	})
	annual, err := c.Periods("annual")
	require.NoError(t, err)
	assertPeriods(t, annual, []want{{"Y1", d(2025, 4, 1), d(2026, 3, 31)}})
}

// ---- fail closed ------------------------------------------------------------

func TestCalendar_FailClosed(t *testing.T) {
	t.Parallel()

	_, err := NewCalendar("custom", "12-31", "", "", nil, 2026)
	assert.ErrorContains(t, err, "no periods")

	_, err = NewCalendar("lunar", "12-31", "", "", nil, 2026)
	assert.ErrorContains(t, err, "unknown fiscal calendar pattern")

	_, err = NewCalendar("445", "12-31", "someday", "nearest", nil, 2026)
	assert.ErrorContains(t, err, "fiscalWeekEndDay")

	_, err = NewCalendar("445", "12-31", "saturday", "closest", nil, 2026)
	assert.ErrorContains(t, err, "fiscalYearEndRule")

	_, err = NewCalendar("standard", "13-01", "", "", nil, 2026)
	assert.ErrorContains(t, err, "financialYearEnd")
	_, err = NewCalendar("standard", "1-1", "", "", nil, 2026)
	assert.ErrorContains(t, err, "financialYearEnd")

	custom := mustCalendar(t, "custom", "12-31", "", "", []CustomPeriod{{Code: "X", Name: "X", StartDate: "01-01", EndDate: "12-31"}}, 2026)
	_, err = custom.Periods("weekly")
	assert.ErrorContains(t, err, "weekly")
	_, err = custom.Periods("fortnightly")
	assert.ErrorContains(t, err, "unsupported periodicity")

	std := mustCalendar(t, "standard", "12-31", "", "", nil, 2026)
	_, err = std.Periods("fortnightly")
	assert.ErrorContains(t, err, "unsupported periodicity")
	for _, tc := range []struct{ code, periodicity string }{
		{"M13", "monthly"}, {"M0", "monthly"}, {"Q5", "quarterly"}, {"H3", "bi-annual"},
		{"Y2", "annual"}, {"W53", "weekly"}, {"P1", "monthly"}, {"", "monthly"},
	} {
		_, err = std.PeriodEnd(tc.code, tc.periodicity)
		assert.ErrorContains(t, err, tc.code, "%s/%s must be refused", tc.code, tc.periodicity)
	}

	w := mustCalendar(t, "445", "01-31", "saturday", "nearest", nil, 2024)
	_, err = w.Period("M13", "monthly")
	assert.ErrorContains(t, err, `"M13"`)
	_, err = w.Periods("fortnightly")
	assert.ErrorContains(t, err, "unsupported periodicity")

	// A custom period with a malformed date surfaces on use, naming the period.
	bad := mustCalendar(t, "custom", "12-31", "", "", []CustomPeriod{{Code: "X", Name: "X", StartDate: "01-01", EndDate: "13-31"}}, 2026)
	_, err = bad.Periods("monthly")
	assert.ErrorContains(t, err, `"X"`)
}

// ---- payment helpers ----------------------------------------------------------

func TestApplyMonthDayOffset(t *testing.T) {
	t.Parallel()
	// 31 Jan + 1 month → 28 Feb (clamped), + 10 days → 10 Mar.
	assert.Equal(t, d(2025, 3, 10), ApplyMonthDayOffset(d(2025, 1, 31), 1, 10))
	assert.Equal(t, d(2025, 1, 31), ApplyMonthDayOffset(d(2025, 1, 31), 0, 0))
	assert.Equal(t, d(2026, 1, 15), ApplyMonthDayOffset(d(2025, 12, 31), 0, 15))
}

func TestFirstFixedDateOnOrAfter(t *testing.T) {
	t.Parallel()
	got, err := FirstFixedDateOnOrAfter(d(2025, 3, 31), []string{"03-31", "09-30"})
	require.NoError(t, err)
	assert.Equal(t, d(2025, 3, 31), got, "on the period end counts")

	got, err = FirstFixedDateOnOrAfter(d(2025, 4, 30), []string{"03-31", "09-30"})
	require.NoError(t, err)
	assert.Equal(t, d(2025, 9, 30), got)

	got, err = FirstFixedDateOnOrAfter(d(2025, 10, 31), []string{"03-31", "09-30"})
	require.NoError(t, err)
	assert.Equal(t, d(2026, 3, 31), got, "wraps into the next year")

	got, err = FirstFixedDateOnOrAfter(d(2025, 12, 31), []string{"09-30", "03-31"})
	require.NoError(t, err)
	assert.Equal(t, d(2026, 3, 31), got, "order of the list does not matter")

	_, err = FirstFixedDateOnOrAfter(d(2025, 1, 1), nil)
	assert.Error(t, err)
	_, err = FirstFixedDateOnOrAfter(d(2025, 1, 1), []string{"31-03"})
	assert.ErrorContains(t, err, `"31-03"`)
}

func TestParseMonthDay(t *testing.T) {
	t.Parallel()
	m, day, err := ParseMonthDay("02-29")
	require.NoError(t, err)
	assert.Equal(t, 2, m)
	assert.Equal(t, 29, day)
	for _, bad := range []string{"", "2-29", "02-30", "00-10", "13-01", "04-31", "12-3", "ab-cd", "12/31", "12-31 "} {
		_, _, err := ParseMonthDay(bad)
		assert.Error(t, err, bad)
	}
	// 02-29 clamps to 28 Feb outside leap years when placed.
	assert.Equal(t, d(2025, 2, 28), placeMonthDay(2025, 2, 29))
	assert.Equal(t, d(2024, 2, 29), placeMonthDay(2024, 2, 29))
}

// An ordered custom table whose last period runs into January (a 52-week
// retail year expressed as MM-DD) is placed monotonically: the trailing
// period moves to the following calendar year instead of a year early.
func TestCalendar_CustomPeriodsMonotonicAcrossJanuary(t *testing.T) {
	t.Parallel()
	periods := []CustomPeriod{
		{Code: "P1", Name: "Period 1", StartDate: "01-04", EndDate: "01-31"},
		{Code: "P2", Name: "Period 2", StartDate: "02-01", EndDate: "12-05"},
		{Code: "P3", Name: "Period 3", StartDate: "12-06", EndDate: "01-03"},
	}
	c := mustCalendar(t, "custom", "12-31", "", "", periods, 2025)
	got, err := c.Periods("monthly")
	require.NoError(t, err)
	assertPeriods(t, got, []want{
		{"P1", d(2025, 1, 4), d(2025, 1, 31)},
		{"P2", d(2025, 2, 1), d(2025, 12, 5)},
		{"P3", d(2025, 12, 6), d(2026, 1, 3)},
	})

	// A table that is genuinely out of order fails closed.
	bad := []CustomPeriod{
		{Code: "A", Name: "A", StartDate: "01-01", EndDate: "06-30"},
		{Code: "B", Name: "B", StartDate: "07-01", EndDate: "12-31"},
		{Code: "C", Name: "C", StartDate: "03-01", EndDate: "05-31"},
	}
	c2 := mustCalendar(t, "custom", "12-31", "", "", bad, 2025)
	_, err = c2.Periods("monthly")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `custom period "C" must start on 01-01, the day after "B" ends`)
}
