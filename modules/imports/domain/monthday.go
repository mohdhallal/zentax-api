package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// ParseMonthDay reads a month and a day the way a customer's file writes them,
// and returns the strict MM-DD the API stores (a legal date-only value,
// ADR-0002). The value it returns has been through deadline.ParseMonthDay — the
// same parser the entity and deadline routes use — so nothing this function
// accepts is anything those would refuse.
//
// Accepted:
//   - a month and day with -, / or . between them: "12-31", "9-1", "12/31"
//   - a full date in any of those forms: "2026-12-31", "31/12/2026"
//   - a month NAME in either order: "31 Dec", "December 31", "5-Apr-2026"
//   - a date CELL, which the reading half hands over as "YYYY-MM-DD"
//   - any of the above with a time after it, which is dropped
//
// Ambiguity is refused, never guessed. "05/04" is 5 April to half of Europe and
// 4 May to the rest, and a financial year end silently eleven months out is the
// one mistake an import must not make — so a slash- or dot-separated pair whose
// numbers are both twelve or less and differ comes back as an error naming both
// readings. A DASH-separated PAIR is read as MM-DD: that is the notation the API
// documents and the import template writes, and it is what makes "04-05" the
// 5 April that a great many files carry as their year end.
//
// That exemption is for the pair and only the pair. A value carrying a FOUR-
// DIGIT YEAR was not written in this product's notation — MM-DD has no year in
// it — so the year is positive evidence that somebody's locale wrote the date,
// and "05-04-2026" is dd-mm-yyyy to the same half of Europe that writes
// "05/04/2026". Both are refused, for the same reason and in the same words:
// two spellings of one ambiguity cannot have two answers, and the dashed one is
// the worse of the two to guess at, because the dry run then shows "05-04"
// against a cell that says "05-04-2026" and the transposition is invisible in
// the one place a customer is told to check.
func ParseMonthDay(raw string) (string, error) {
	cleaned := stripTime(CleanCell(raw))
	if cleaned == "" {
		return "", errors.New("it is empty")
	}

	numbers, names, separators, err := splitDateTokens(cleaned)
	if err != nil {
		return "", err
	}

	var month, day int
	switch {
	case len(names) == 1:
		month = names[0]
		day, err = dayFromNumbers(numbers)
	case len(names) > 1:
		return "", errors.New("it names more than one month")
	default:
		month, day, err = monthDayFromNumbers(numbers, separators)
	}
	if err != nil {
		return "", err
	}

	value := fmt.Sprintf("%02d-%02d", month, day)
	if _, _, perr := deadline.ParseMonthDay(value); perr != nil {
		return "", fmt.Errorf("%s is not a date that exists", value)
	}
	return value, nil
}

// stripTime drops a clock time written after the date, which is what a
// spreadsheet exports when a date cell is secretly a timestamp.
func stripTime(s string) string {
	if len(s) > 10 && (s[10] == 'T' || s[10] == 't') && isISOPrefix(s) {
		return s[:10]
	}
	fields := strings.Fields(s)
	kept := fields[:0]
	for _, f := range fields {
		if strings.ContainsRune(f, ':') {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

func isISOPrefix(s string) bool {
	for i, r := range s[:10] {
		if i == 4 || i == 7 {
			if r != '-' {
				return false
			}
			continue
		}
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// splitDateTokens breaks a date into its numbers and its month names, and
// reports which separators held them together — the separator is what decides
// a two-number value that could be read either way round.
func splitDateTokens(s string) (numbers []int, names []int, separators string, err error) {
	var seps strings.Builder
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		if r == '-' || r == '/' || r == '.' || r == ',' || unicode.IsSpace(r) {
			seps.WriteRune(r)
			return true
		}
		return false
	}) {
		if n, convErr := strconv.Atoi(part); convErr == nil {
			numbers = append(numbers, n)
			continue
		}
		if month, ok := monthByName(part); ok {
			names = append(names, month)
			continue
		}
		return nil, nil, "", fmt.Errorf("%q is not a number or a month name", part)
	}
	if len(numbers)+len(names) == 0 {
		return nil, nil, "", errors.New("it holds no date at all")
	}
	return numbers, names, seps.String(), nil
}

// dayFromNumbers picks the day out of the numbers beside a month name, allowing
// a four-digit year to sit alongside it.
func dayFromNumbers(numbers []int) (int, error) {
	var days []int
	for _, n := range numbers {
		if isYear(n) {
			continue
		}
		days = append(days, n)
	}
	if len(days) != 1 {
		return 0, errors.New("a month name needs exactly one day beside it")
	}
	return days[0], nil
}

// monthDayFromNumbers reads a month and a day out of two or three numbers.
func monthDayFromNumbers(numbers []int, separators string) (month, day int, err error) {
	switch len(numbers) {
	case 2:
		// A pair, where the separator decides: a dash is this product's own
		// MM-DD notation, and a locale separator settles nothing.
		return orderMonthDay(numbers[0], numbers[1], strings.ContainsAny(separators, "/."))
	case 3:
		return monthDayFromFullDate(numbers)
	case 1:
		if numbers[0] >= plausibleYearMin && numbers[0] <= plausibleYearMax {
			return 0, 0, errors.New("it is a year on its own, with no month or day")
		}
		return 0, 0, errors.New("it is a single number; write the month and the day as MM-DD " +
			"(a spreadsheet date column saved as plain numbers lands here)")
	default:
		return 0, 0, errors.New("it has too many parts to be a month and a day")
	}
}

// monthDayFromFullDate reads the month and the day out of a date that carries
// its year.
//
// The separator has no say here, whatever it is. MM-DD is the notation this
// product documents and its template writes, and that notation has no year in
// it — so a three-number value was written by somebody's locale, not by this
// product, and "05-04-2026" is dd-mm-yyyy wherever "05/04/2026" is. Excel's own
// `dd-mm-yyyy` custom format and a great many ERP exports write exactly this
// string. Reading it month-first was a guess with no evidence behind it, made
// in the one place where being eleven months wrong is invisible: the dry run
// shows "05-04" for a cell that reads "05-04-2026".
func monthDayFromFullDate(numbers []int) (month, day int, err error) {
	switch {
	case isYear(numbers[0]) && !isYear(numbers[2]):
		// Year first is ISO, and ISO is never ambiguous.
		return numbers[1], numbers[2], nil
	case isYear(numbers[2]) && !isYear(numbers[0]):
		return orderMonthDay(numbers[0], numbers[1], true)
	default:
		return 0, 0, errors.New("its three numbers are not a year, a month and a day")
	}
}

// orderMonthDay decides which of two numbers is the month. One of them being
// larger than twelve settles it, and so does their being the same number;
// otherwise the caller says whether the file's own notation leaves the order
// open, and a value with two readings is refused with both of them named.
func orderMonthDay(first, second int, ambiguous bool) (month, day int, err error) {
	firstCanBeMonth := first >= 1 && first <= monthsInYear
	secondCanBeMonth := second >= 1 && second <= monthsInYear
	switch {
	case firstCanBeMonth && !secondCanBeMonth:
		return first, second, nil
	case secondCanBeMonth && !firstCanBeMonth:
		return second, first, nil
	case !firstCanBeMonth && !secondCanBeMonth:
		return 0, 0, errors.New("neither of its two numbers can be a month")
	case first == second:
		return first, second, nil
	case ambiguous:
		return 0, 0, fmt.Errorf("it could be %s or %s, and the file does not say which",
			spellMonthDay(first, second), spellMonthDay(second, first))
	default:
		// A dash-separated pair: the notation the API documents, month first.
		return first, second, nil
	}
}

const (
	monthsInYear = 12
	// minYear / maxYear are what a four-digit number beside a month and a day
	// is taken to be; plausibleYearMin / plausibleYearMax are the narrower range
	// a number ON ITS OWN is called a year rather than a mistyped MM-DD, so
	// "1231" is read as somebody's 12-31 rather than as the year 1231.
	minYear          = 1000
	maxYear          = 9999
	plausibleYearMin = 1900
	plausibleYearMax = 2200
	monthNameSize    = 3
)

func isYear(n int) bool { return n >= minYear && n <= maxYear }

// spellMonthDay writes one reading of an ambiguous value both ways round, so
// the person fixing the file can see which one they meant: "4 March (03-04)".
func spellMonthDay(month, day int) string {
	return fmt.Sprintf("%d %s (%02d-%02d)", day, monthTitles[month-1], month, day)
}

var monthTitles = [12]string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

// monthNames covers the English spellings a file is written in. A name this map
// does not know is refused by name, so a localised export fails loudly instead
// of landing in the wrong month.
var monthNames = map[string]int{
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	"sept": 9,
}

func monthByName(part string) (int, bool) {
	name := strings.ToLower(strings.TrimSuffix(CleanCell(part), "."))
	if month, ok := monthNames[name]; ok {
		return month, true
	}
	if len(name) == monthNameSize {
		for full, month := range monthNames {
			if strings.HasPrefix(full, name) {
				return month, true
			}
		}
	}
	return 0, false
}
