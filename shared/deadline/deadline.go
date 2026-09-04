// Package deadline computes tax period boundaries and filing / payment
// deadlines for every fiscal calendar pattern (ADR-0023). It is pure civil-date
// arithmetic over dateonly.Date — no instant, no timezone, anywhere (ADR-0002).
//
// A Calendar is built from an entity's fiscal configuration plus the fiscal
// year (the calendar year the year ENDS in) and yields the period list for a
// periodicity, or one period by code. The generator and GET /entities/{id}/
// periods use the same Calendar, so what the UI shows is what start persists
// (ADR-0001). Anything the engine cannot compute is an error — callers fail
// closed rather than emit an approximate (wrong) date.
package deadline

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Fiscal calendar patterns.
const (
	PatternStandard    = "standard"
	Pattern445         = "445"
	Pattern454         = "454"
	Pattern544         = "544"
	Pattern13Period    = "13-period"
	PatternWeekly      = "weekly"
	PatternCustom      = "custom"
	YearEndRuleLast    = "last"
	YearEndRuleNearest = "nearest"
)

// Periodicities.
const (
	PeriodicityWeekly             = "weekly"
	PeriodicityMonthly            = "monthly"
	PeriodicityQuarterly          = "quarterly"
	PeriodicityBiAnnual           = "bi-annual"
	PeriodicityAnnual             = "annual"
	PeriodicityConsolidatedAnnual = "consolidated-annual"
)

// Period is one reporting period of a fiscal year: a stable code (M1, Q4, W53,
// P13, Y1, CY1 or a custom code), a short deterministic label and inclusive
// civil start / end dates.
type Period struct {
	Code  string
	Label string
	Start dateonly.Date
	End   dateonly.Date
}

// CustomPeriod is one entry of a custom calendar: MM-DD start / end placed
// into the fiscal year by the standard year rule.
type CustomPeriod struct {
	Code      string
	Name      string
	StartDate string // MM-DD
	EndDate   string // MM-DD
}

// Calendar is an entity's fiscal calendar resolved for one fiscal year. Build
// it with NewCalendar; the zero value is not usable.
type Calendar struct {
	pattern       string
	fyEndMonth    int // 1-12
	fyEndDay      int // 1-31 (the anchor day for week-based patterns)
	startMonth    int // fiscal-year start month (standard / custom placement)
	weekEndDay    time.Weekday
	yearEndRule   string
	customPeriods []CustomPeriod
	fiscalYear    int // the calendar year the fiscal year ends in

	// Resolved for week-based patterns only.
	yearStart dateonly.Date
	yearEnd   dateonly.Date
	weeks     int // 52 or 53
}

// NewCalendar validates the configuration and resolves the fiscal year.
//   - pattern: standard | 445 | 454 | 544 | 13-period | weekly | custom ("" = standard)
//   - financialYearEnd: MM-DD ("" = 12-31)
//   - weekEndDay: monday … sunday ("" = saturday); week-based patterns only
//   - yearEndRule: last | nearest ("" = nearest); week-based patterns only
//   - customPeriods: the custom pattern's periods, in order
//   - fiscalYear: the calendar year the fiscal year ends in
//
// A week-based year whose two year ends are neither 364 nor 371 days apart is
// an error (fail closed); so is a custom pattern without periods.
func NewCalendar(pattern, financialYearEnd, weekEndDay, yearEndRule string, customPeriods []CustomPeriod, fiscalYear int) (*Calendar, error) {
	if pattern == "" {
		pattern = PatternStandard
	}
	if !IsSupportedPattern(pattern) {
		return nil, fmt.Errorf("deadline: unknown fiscal calendar pattern %q", pattern)
	}
	if fiscalYear < 1900 || fiscalYear > 2200 {
		return nil, fmt.Errorf("deadline: fiscal year %d out of range", fiscalYear)
	}
	if financialYearEnd == "" {
		financialYearEnd = "12-31"
	}
	mm, dd, err := ParseMonthDay(financialYearEnd)
	if err != nil {
		return nil, fmt.Errorf("deadline: invalid financialYearEnd %q (want MM-DD)", financialYearEnd)
	}
	c := &Calendar{
		pattern:       pattern,
		fyEndMonth:    mm,
		fyEndDay:      dd,
		startMonth:    mm%12 + 1,
		customPeriods: customPeriods,
		fiscalYear:    fiscalYear,
	}

	switch pattern {
	case PatternStandard:
		// Month-based: nothing more to resolve.
	case PatternCustom:
		if len(customPeriods) == 0 {
			return nil, fmt.Errorf("deadline: custom fiscal calendar has no periods")
		}
	default:
		wd, ok := parseWeekday(weekEndDay)
		if !ok {
			return nil, fmt.Errorf("deadline: invalid fiscalWeekEndDay %q", weekEndDay)
		}
		if yearEndRule == "" {
			yearEndRule = YearEndRuleNearest
		}
		if yearEndRule != YearEndRuleLast && yearEndRule != YearEndRuleNearest {
			return nil, fmt.Errorf("deadline: invalid fiscalYearEndRule %q", yearEndRule)
		}
		c.weekEndDay = wd
		c.yearEndRule = yearEndRule
		c.yearEnd = c.weekYearEnd(fiscalYear)
		prev := c.weekYearEnd(fiscalYear - 1)
		c.yearStart = addDays(prev, 1)
		days := daysBetween(prev, c.yearEnd)
		switch days {
		case 364:
			c.weeks = 52
		case 371:
			c.weeks = 53
		default:
			return nil, fmt.Errorf("deadline: fiscal year %d under pattern %q spans %d days (want 364 or 371)", fiscalYear, pattern, days)
		}
	}
	return c, nil
}

// Pattern returns the calendar's pattern.
func (c *Calendar) Pattern() string { return c.pattern }

// FiscalYear returns the calendar year the fiscal year ends in.
func (c *Calendar) FiscalYear() int { return c.fiscalYear }

// Weeks returns the number of weeks (52 or 53) of a week-based year, 0 otherwise.
func (c *Calendar) Weeks() int { return c.weeks }

// YearEnd returns the last day of the fiscal year.
func (c *Calendar) YearEnd() dateonly.Date {
	if c.weeks > 0 {
		return c.yearEnd
	}
	return c.standardYearEnd()
}

// Periods lists the periods of the fiscal year for a periodicity, in order.
func (c *Calendar) Periods(periodicity string) ([]Period, error) {
	switch c.pattern {
	case PatternStandard:
		return c.standardPeriods(periodicity)
	case PatternCustom:
		return c.customPeriodList(periodicity)
	default:
		return c.weekPeriods(periodicity)
	}
}

// Period returns the period with the given code for a periodicity; a code
// outside the pattern is an error naming it.
func (c *Calendar) Period(code, periodicity string) (Period, error) {
	periods, err := c.Periods(periodicity)
	if err != nil {
		return Period{}, err
	}
	for _, p := range periods {
		if p.Code == code {
			return p, nil
		}
	}
	return Period{}, fmt.Errorf("deadline: period code %q is not in the %s calendar for periodicity %q", code, c.pattern, periodicity)
}

// PeriodEnd returns the end date of the period with the given code.
func (c *Calendar) PeriodEnd(code, periodicity string) (dateonly.Date, error) {
	p, err := c.Period(code, periodicity)
	if err != nil {
		return dateonly.Date{}, err
	}
	return p.End, nil
}

// ---- standard (month-based) --------------------------------------------------

func (c *Calendar) fyStartYear() int {
	if c.startMonth == 1 {
		return c.fiscalYear
	}
	return c.fiscalYear - 1
}

// placeMonth returns the calendar year a fiscal-year month falls in (the
// standard year rule): months before the fiscal start month belong to the
// year the fiscal year ends in, the rest to the year it starts in.
func (c *Calendar) placeMonth(month int) int {
	if month < c.startMonth {
		return c.fiscalYear
	}
	return c.fyStartYear()
}

func (c *Calendar) standardYearEnd() dateonly.Date {
	return dateonly.New(c.fiscalYear, c.fyEndMonth, lastDayOfMonth(c.fiscalYear, c.fyEndMonth))
}

func (c *Calendar) standardYearStart() dateonly.Date {
	return dateonly.New(c.fyStartYear(), c.startMonth, 1)
}

// fiscalMonthRange returns the first day of fiscal month `from` and the last
// day of fiscal month `to` (1-12).
func (c *Calendar) fiscalMonthRange(from, to int) (dateonly.Date, dateonly.Date) {
	fm := calendarMonth(from, c.startMonth)
	tm := calendarMonth(to, c.startMonth)
	fy := c.placeMonth(fm)
	ty := c.placeMonth(tm)
	return dateonly.New(fy, fm, 1), dateonly.New(ty, tm, lastDayOfMonth(ty, tm))
}

func (c *Calendar) standardPeriods(periodicity string) ([]Period, error) {
	switch periodicity {
	case PeriodicityMonthly:
		return c.monthGroups("M", 1, 12), nil
	case PeriodicityQuarterly:
		return c.monthGroups("Q", 3, 4), nil
	case PeriodicityBiAnnual:
		return c.monthGroups("H", 6, 2), nil
	case PeriodicityAnnual:
		return c.monthGroups("Y", 12, 1), nil
	case PeriodicityConsolidatedAnnual:
		return c.monthGroups("CY", 12, 1), nil
	case PeriodicityWeekly:
		// Seven-day weeks from the fiscal-year start; the last week absorbs
		// the remainder so W52 ends on the fiscal-year end.
		start := c.standardYearStart()
		end := c.standardYearEnd()
		out := make([]Period, 0, 52)
		for w := 1; w <= 52; w++ {
			ws := addDays(start, (w-1)*7)
			we := addDays(ws, 6)
			if w == 52 {
				we = end
			}
			out = append(out, newPeriod("W"+strconv.Itoa(w), "", ws, we))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("deadline: unsupported periodicity %q", periodicity)
	}
}

// monthGroups builds n periods of `months` fiscal months each.
func (c *Calendar) monthGroups(prefix string, months, n int) []Period {
	out := make([]Period, 0, n)
	for i := 1; i <= n; i++ {
		s, e := c.fiscalMonthRange((i-1)*months+1, i*months)
		out = append(out, newPeriod(prefix+strconv.Itoa(i), "", s, e))
	}
	return out
}

// ---- week-based (445 / 454 / 544 / 13-period / weekly) ------------------------

// weekYearEnd resolves the fiscal-year end for the calendar year `year`: the
// week-end day on/before the anchor ("last") or the one closest to it
// ("nearest"; with seven-day weeks the two candidates are never equidistant,
// and a three-day gap resolves to the earlier one).
func (c *Calendar) weekYearEnd(year int) dateonly.Date {
	anchor := placeMonthDay(year, c.fyEndMonth, c.fyEndDay)
	diff := (int(anchor.Time().Weekday()) - int(c.weekEndDay) + 7) % 7
	before := addDays(anchor, -diff)
	if c.yearEndRule == YearEndRuleLast || diff <= 3 {
		return before
	}
	return addDays(before, 7)
}

// weekPattern returns the number of weeks per period for the monthly grouping.
func (c *Calendar) weekPattern() []int {
	switch c.pattern {
	case Pattern454:
		return repeatQuarters([]int{4, 5, 4})
	case Pattern544:
		return repeatQuarters([]int{5, 4, 4})
	case Pattern13Period:
		return []int{4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4}
	default: // 445 and weekly (monthly grouping [4,4,5])
		return repeatQuarters([]int{4, 4, 5})
	}
}

func repeatQuarters(q []int) []int {
	out := make([]int, 0, 12)
	for i := 0; i < 4; i++ {
		out = append(out, q...)
	}
	return out
}

func (c *Calendar) weekPeriods(periodicity string) ([]Period, error) {
	var (
		prefix string
		weeks  []int
	)
	switch periodicity {
	case PeriodicityMonthly:
		prefix = "M"
		if c.pattern == Pattern13Period {
			prefix = "P"
		}
		weeks = c.weekPattern()
	case PeriodicityQuarterly:
		prefix, weeks = "Q", []int{13, 13, 13, 13}
	case PeriodicityBiAnnual:
		prefix, weeks = "H", []int{26, 26}
	case PeriodicityAnnual:
		prefix, weeks = "Y", []int{52}
	case PeriodicityConsolidatedAnnual:
		prefix, weeks = "CY", []int{52}
	case PeriodicityWeekly:
		prefix = "W"
		weeks = make([]int, c.weeks)
		for i := range weeks {
			weeks[i] = 1
		}
	default:
		return nil, fmt.Errorf("deadline: unsupported periodicity %q", periodicity)
	}
	// The 53rd week is added to the LAST period of the year (for the weekly
	// periodicity it already is its own W53).
	if c.weeks == 53 && periodicity != PeriodicityWeekly {
		weeks = append([]int(nil), weeks...)
		weeks[len(weeks)-1]++
	}

	out := make([]Period, 0, len(weeks))
	start := c.yearStart
	for i, w := range weeks {
		end := addDays(start, w*7-1)
		out = append(out, newPeriod(prefix+strconv.Itoa(i+1), "", start, end))
		start = addDays(end, 1)
	}
	return out, nil
}

// ---- custom -----------------------------------------------------------------

func (c *Calendar) customPeriodList(periodicity string) ([]Period, error) {
	switch periodicity {
	case PeriodicityMonthly, PeriodicityQuarterly, PeriodicityBiAnnual:
		// The listed periods ARE the calendar; the periodicity is informational.
		out := make([]Period, 0, len(c.customPeriods))
		for i, cp := range c.customPeriods {
			em, ed, err := ParseMonthDay(cp.EndDate)
			if err != nil {
				return nil, fmt.Errorf("deadline: custom period %q has invalid endDate %q (want MM-DD)", cp.Code, cp.EndDate)
			}
			sm, sd, err := ParseMonthDay(cp.StartDate)
			if err != nil {
				return nil, fmt.Errorf("deadline: custom period %q has invalid startDate %q (want MM-DD)", cp.Code, cp.StartDate)
			}
			var start, end dateonly.Date
			if i == 0 {
				// The first period is placed by the standard year rule; a start
				// after its end straddles the year boundary (start a year earlier).
				end = placeMonthDay(c.placeMonth(em), em, ed)
				start = placeMonthDay(c.placeMonth(sm), sm, sd)
				if start.Time().After(end.Time()) {
					start = placeMonthDay(start.Year-1, sm, sd)
				}
			} else {
				// The list is ordered and tiles the year (ADR-0023 §3): every
				// later period starts the day after its predecessor ends, which
				// pins its calendar year — a trailing period that runs into
				// January lands in the next year, and a gap or overlap (an
				// out-of-order table) fails closed.
				start = addDays(out[i-1].End, 1)
				if start.Month != sm || start.Day != sd {
					return nil, fmt.Errorf("deadline: custom period %q must start on %02d-%02d, the day after %q ends",
						cp.Code, start.Month, start.Day, out[i-1].Code)
				}
				end = placeMonthDay(start.Year, em, ed)
				if end.Time().Before(start.Time()) {
					end = placeMonthDay(start.Year+1, em, ed)
				}
			}
			out = append(out, newPeriod(cp.Code, cp.Name, start, end))
		}
		return out, nil
	case PeriodicityAnnual:
		return []Period{newPeriod("Y1", "", c.standardYearStart(), c.standardYearEnd())}, nil
	case PeriodicityConsolidatedAnnual:
		return []Period{newPeriod("CY1", "", c.standardYearStart(), c.standardYearEnd())}, nil
	case PeriodicityWeekly:
		return nil, fmt.Errorf("deadline: weekly periods are not defined for a custom fiscal calendar")
	default:
		return nil, fmt.Errorf("deadline: unsupported periodicity %q", periodicity)
	}
}

// ---- labels -----------------------------------------------------------------

func newPeriod(code, name string, start, end dateonly.Date) Period {
	label := code
	if name != "" {
		label = name
	}
	return Period{Code: code, Label: label + " (" + formatRange(start, end) + ")", Start: start, End: end}
}

var monthAbbr = [...]string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// formatRange renders "1 Jan – 31 Jan 2025" or "29 Dec 2024 – 25 Jan 2025".
func formatRange(start, end dateonly.Date) string {
	if start.Year == end.Year {
		return fmt.Sprintf("%d %s – %d %s %d", start.Day, monthAbbr[start.Month], end.Day, monthAbbr[end.Month], end.Year)
	}
	return fmt.Sprintf("%d %s %d – %d %s %d", start.Day, monthAbbr[start.Month], start.Year, end.Day, monthAbbr[end.Month], end.Year)
}

// ---- legacy standard helpers (kept for callers and existing tests) -----------

// IsSupportedPattern reports whether the fiscal calendar pattern is one the
// engine knows. Every pattern is computed now; the engine still errors on a
// year or period it cannot derive.
func IsSupportedPattern(pattern string) bool {
	switch pattern {
	case "", PatternStandard, Pattern445, Pattern454, Pattern544, Pattern13Period, PatternWeekly, PatternCustom:
		return true
	}
	return false
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

// PeriodEndDate computes the calendar end date of a STANDARD-calendar period
// given its code (e.g. "M1", "Q1", "H2", "Y1"), the periodicity, the entity's
// fiscal-year start month, and the calendar year the fiscal year ends.
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
	case PeriodicityMonthly:
		if n < 1 || n > 12 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n, nil
	case PeriodicityQuarterly:
		if n < 1 || n > 4 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n * 3, nil
	case PeriodicityBiAnnual:
		if n < 1 || n > 2 {
			return 0, badPeriod(periodCode, periodicity)
		}
		return n * 6, nil
	case PeriodicityAnnual, PeriodicityConsolidatedAnnual:
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

// ---- offsets and adjustments ---------------------------------------------------

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

// ApplyMonthDayOffset adds whole months (month-end clamped) and then days —
// the "N months + M days after period end" form of the obligation builder.
func ApplyMonthDayOffset(d dateonly.Date, months, days int) dateonly.Date {
	return addDays(addMonthsClamped(d, months), days)
}

// FirstFixedDateOnOrAfter returns the earliest of the MM-DD dates placed on
// or after `from` (each is tried in from's year, then the next year). An
// invalid MM-DD is an error; an empty list is an error.
func FirstFixedDateOnOrAfter(from dateonly.Date, fixedDates []string) (dateonly.Date, error) {
	if len(fixedDates) == 0 {
		return dateonly.Date{}, fmt.Errorf("deadline: no fixed dates")
	}
	candidates := make([]dateonly.Date, 0, len(fixedDates))
	for _, md := range fixedDates {
		mm, dd, err := ParseMonthDay(md)
		if err != nil {
			return dateonly.Date{}, fmt.Errorf("deadline: invalid fixed date %q (want MM-DD)", md)
		}
		d := placeMonthDay(from.Year, mm, dd)
		if d.Time().Before(from.Time()) {
			d = placeMonthDay(from.Year+1, mm, dd)
		}
		candidates = append(candidates, d)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Time().Before(candidates[j].Time()) })
	return candidates[0], nil
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

// ---- MM-DD and weekday parsing ---------------------------------------------------

// ParseMonthDay parses a strict "MM-DD" (two-digit month 01-12, two-digit day
// valid for that month; 02-29 is accepted — it clamps to the 28th in a
// non-leap year when placed).
func ParseMonthDay(s string) (month, day int, err error) {
	if len(s) != 5 || s[2] != '-' || !isDigits(s[:2]) || !isDigits(s[3:]) {
		return 0, 0, fmt.Errorf("deadline: invalid MM-DD %q", s)
	}
	month, _ = strconv.Atoi(s[:2])
	day, _ = strconv.Atoi(s[3:])
	if month < 1 || month > 12 || day < 1 || day > maxDayOfMonth(month) {
		return 0, 0, fmt.Errorf("deadline: invalid MM-DD %q", s)
	}
	return month, day, nil
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// maxDayOfMonth is the longest the month ever is (Feb = 29).
func maxDayOfMonth(month int) int {
	switch month {
	case 2:
		return 29
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

// placeMonthDay builds a date in `year`, clamping 02-29 to 02-28 outside leap years.
func placeMonthDay(year, month, day int) dateonly.Date {
	if last := lastDayOfMonth(year, month); day > last {
		day = last
	}
	return dateonly.New(year, month, day)
}

var weekdays = map[string]time.Weekday{
	"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday, "sunday": time.Sunday,
}

func parseWeekday(s string) (time.Weekday, bool) {
	if s == "" {
		return time.Saturday, true
	}
	wd, ok := weekdays[s]
	return wd, ok
}

// IsWeekEndDay reports whether s is one of monday … sunday.
func IsWeekEndDay(s string) bool {
	_, ok := weekdays[s]
	return ok
}

// ---- date arithmetic ------------------------------------------------------------

func addDays(d dateonly.Date, days int) dateonly.Date {
	return dateonly.FromTime(d.Time().AddDate(0, 0, days))
}

func daysBetween(a, b dateonly.Date) int {
	return int(b.Time().Sub(a.Time()).Hours() / 24)
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
