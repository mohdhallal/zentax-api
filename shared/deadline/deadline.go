// Package deadline computes tax period-end dates and filing/task deadlines. It
// is a Go port of the frontend's tested fiscal-calendar.ts + deadline-utils.ts,
// scoped to STANDARD fiscal calendars; non-standard patterns (445/454/13-period/
// weekly/custom) are not yet supported and callers must fail closed rather than
// emit an approximate (wrong) deadline.
package deadline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// IsSupportedPattern reports whether the fiscal calendar pattern can be computed.
// Only the standard (calendar-based) pattern is supported so far.
func IsSupportedPattern(pattern string) bool {
	return pattern == "" || pattern == "standard"
}

// FiscalYearStartMonth derives the fiscal-year start month (1-12) from an
// entity's financialYearEnd ("MM-DD"). "" means a calendar year (starts January).
func FiscalYearStartMonth(financialYearEnd string) (int, error) {
	if financialYearEnd == "" {
		return 1, nil
	}
	parts := strings.SplitN(financialYearEnd, "-", 2)
	mm, err := strconv.Atoi(parts[0])
	if err != nil || mm < 1 || mm > 12 {
		return 0, fmt.Errorf("deadline: invalid financialYearEnd %q (want MM-DD)", financialYearEnd)
	}
	return mm%12 + 1, nil
}

// calendarMonth maps a fiscal month (1-12) to a calendar month (1-12).
func calendarMonth(fiscalMonth, fiscalYearStartMonth int) int {
	return (fiscalYearStartMonth+fiscalMonth-2)%12 + 1
}

// PeriodEndDate computes the calendar end date of a period given its code
// (e.g. "M1", "Q1", "H2", "Y1"), the periodicity, the entity's fiscal-year start
// month, and the calendar year the fiscal year ends.
func PeriodEndDate(periodCode, periodicity string, fiscalYearStartMonth, fiscalYearEndYear int) (dateonly.Date, error) {
	lfm, err := lastFiscalMonth(periodCode, periodicity)
	if err != nil {
		return dateonly.Date{}, err
	}
	cal := calendarMonth(lfm, fiscalYearStartMonth)

	fyStartYear := fiscalYearEndYear
	if fiscalYearStartMonth != 1 {
		fyStartYear = fiscalYearEndYear - 1
	}
	year := fyStartYear
	if cal < fiscalYearStartMonth {
		year = fiscalYearEndYear
	}
	return dateonly.New(year, cal, lastDayOfMonth(year, cal)), nil
}

// lastFiscalMonth decodes a period code + periodicity to the LAST fiscal month
// (1-12) the period covers.
func lastFiscalMonth(periodCode, periodicity string) (int, error) {
	n, err := periodNumber(periodCode)
	if err != nil {
		return 0, err
	}
	switch periodicity {
	case "monthly":
		if n < 1 || n > 12 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n, nil
	case "quarterly":
		if n < 1 || n > 4 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n * 3, nil
	case "bi-annual":
		if n < 1 || n > 2 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n * 6, nil
	case "annual", "consolidated-annual":
		return 12, nil
	default:
		return 0, fmt.Errorf("deadline: unsupported periodicity %q", periodicity)
	}
}

func periodNumber(code string) (int, error) {
	i := 0
	for i < len(code) && (code[i] < '0' || code[i] > '9') {
		i++
	}
	if i == len(code) {
		return 0, fmt.Errorf("deadline: period code %q has no number", code)
	}
	n, err := strconv.Atoi(code[i:])
	if err != nil {
		return 0, fmt.Errorf("deadline: invalid period code %q", code)
	}
	return n, nil
}

func badPeriod(code, periodicity string) error {
	return fmt.Errorf("deadline: period code %q out of range for periodicity %q", code, periodicity)
}

// ApplyOffset adds value (days | weeks | months) to a date in the given
// direction (before | after), clamping month-ends (Mar 31 + 1mo -> Apr 30).
func ApplyOffset(d dateonly.Date, value int, unit, direction string) dateonly.Date {
	mult := 1
	if direction == "before" {
		mult = -1
	}
	switch unit {
	case "days":
		return addDays(d, value*mult)
	case "weeks":
		return addDays(d, value*7*mult)
	case "months":
		return addMonthsClamped(d, value*mult)
	default:
		return d
	}
}

// ApplyWeekendAdjustment moves a deadline off a weekend per the adjustment
// ("none" | "next-business-day" | "prev-business-day").
func ApplyWeekendAdjustment(d dateonly.Date, adjustment string) dateonly.Date {
	if adjustment == "" || adjustment == "none" {
		return d
	}
	switch d.Time().Weekday() {
	case time.Saturday:
		if adjustment == "next-business-day" {
			return addDays(d, 2)
		}
		return addDays(d, -1)
	case time.Sunday:
		if adjustment == "next-business-day" {
			return addDays(d, 1)
		}
		return addDays(d, -2)
	default:
		return d
	}
}

func addDays(d dateonly.Date, days int) dateonly.Date {
	return dateonly.FromTime(d.Time().AddDate(0, 0, days))
}

// addMonthsClamped adds whole months, clamping the day to the target month's
// last day so a month-end never overflows (mirrors shared/date-utils.ts).
func addMonthsClamped(d dateonly.Date, months int) dateonly.Date {
	targetFirst := time.Date(d.Year, time.Month(d.Month)+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := lastDayOfMonth(targetFirst.Year(), int(targetFirst.Month()))
	day := d.Day
	if day > last {
		day = last
	}
	return dateonly.New(targetFirst.Year(), int(targetFirst.Month()), day)
}

func lastDayOfMonth(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
