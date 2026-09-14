package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The notations a customer's file actually writes a month and a day in, and
// what each one has to mean.
func TestParseMonthDay_AcceptsWhatRealFilesWrite(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"12-31":                "12-31", // The notation the API documents.
		"09-30":                "09-30",
		"9-30":                 "09-30", // Unpadded.
		"04-05":                "04-05", // Dash-separated: MM-DD, so 5 April — the UK tax year end.
		"31-12":                "12-31", // Reversed, settled by 31 not being a month.
		"31/12":                "12-31",
		"31.12.":               "12-31", // A German file, trailing ordinal dot.
		"2026-12-31":           "12-31", // ISO, which is never ambiguous.
		"2026-12-31T00:00:00Z": "12-31", // A timestamp a system exported.
		"2026-12-31 00:00:00":  "12-31",
		"31/12/2026":           "12-31", // Day first, settled by 31.
		"12/31/2026":           "12-31", // Month first, settled by 31.
		"31 December":          "12-31",
		"December 31":          "12-31",
		"31-Dec-2026":          "12-31",
		"Dec 31":               "12-31",
		"5 April":              "04-05",
		"5-Apr":                "04-05",
		"1 Sept":               "09-01",
		"  12-31  ":            "12-31", // Padding.
		"02-29":                "02-29", // A leap day, which the engine clamps when it places it.
	}
	for input, want := range cases {
		got, err := ParseMonthDay(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

// A value that could be read two ways is refused with both readings spelled
// out. Guessing here would put a financial year end months away from the truth
// and nothing downstream would ever notice.
func TestParseMonthDay_RefusesAnAmbiguousValue(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"05/04", "03/04", "01.12", "05/04/2026", "1/2/2026"} {
		_, err := ParseMonthDay(input)
		require.Error(t, err, input)
		assert.Contains(t, err.Error(), "could be", input)
		assert.Contains(t, err.Error(), "does not say which", input)
	}

	_, err := ParseMonthDay("03/04")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4 March (03-04)")
	assert.Contains(t, err.Error(), "3 April (04-03)")
}

// A full date whose two remaining numbers could each be a month is refused
// however it is punctuated. It used to depend on the separator: "05/04/2026"
// was refused naming both readings while "05-04-2026" was read month-first,
// silently, so the same calendar date had two answers in one file.
//
// The dash exemption belongs to the PAIR, and to the reason for it: MM-DD is
// the notation this product documents and its template writes, and that
// notation has no year in it. A value carrying a four-digit year was written by
// somebody's locale — dd-mm-yyyy is what Excel's own custom format and a great
// many ERP exports produce — so there is nothing left to prefer one reading
// over the other.
//
// This is also the worse half of the two to guess at. The dry run shows "05-04"
// for a cell that reads "05-04-2026": the same two numbers in the same order,
// so the transposition is invisible in the one place the guide sends a customer
// to check, and the year end lands eleven months from the truth.
func TestParseMonthDay_AFullDateIsAmbiguousWhateverSeparatesIt(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"05-04-2026", "01-02-2026", "5-4-2026", "03-04-2026"} {
		_, err := ParseMonthDay(input)
		require.Error(t, err, input)
		assert.Contains(t, err.Error(), "could be", input)
		assert.Contains(t, err.Error(), "does not say which", input)
	}

	_, dashed := ParseMonthDay("05-04-2026")
	require.Error(t, dashed)
	_, slashed := ParseMonthDay("05/04/2026")
	require.Error(t, slashed)
	assert.Equal(t, slashed.Error(), dashed.Error(),
		"one ambiguity, one answer: the two spellings are refused in the same words")
	assert.Contains(t, dashed.Error(), "4 May (05-04)")
	assert.Contains(t, dashed.Error(), "5 April (04-05)")
}

// What the refusal above must NOT take with it: a full date only one of whose
// numbers can be a month is settled by that, and a dash-separated PAIR keeps the
// product's own MM-DD reading — it is what a great many files carry as their
// year end, and the template writes it.
func TestParseMonthDay_AFullDateThatIsNotAmbiguousStillReads(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"31-12-2026":  "12-31", // Day first, settled by 31.
		"12-31-2026":  "12-31", // Month first, settled by 31.
		"01-01-2026":  "01-01", // The same number twice reads the same either way round.
		"2026-04-05":  "04-05", // ISO, which is never ambiguous.
		"31-Dec-2026": "12-31", // A month NAME settles it whatever the separator.
		"5-Apr-2026":  "04-05",
		"04-05":       "04-05", // The pair, and the notation this product writes.
		"1-2":         "01-02",
		"2026-12-31":  "12-31",
		"31.12.2026":  "12-31",
		"29-02-2028":  "02-29",
		"2026-02-29":  "02-29",
		"12-31-2026 ": "12-31",
		// Two digits are not a year, so this is three numbers that are not a
		// date at all — refused, as it was before, for that reason.
		"05-04-26": "",
	}
	for input, want := range cases {
		got, err := ParseMonthDay(input)
		if want == "" {
			require.Error(t, err, input)
			continue
		}
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

// A slash-separated pair only one of whose numbers can be a month is not
// ambiguous, and is read rather than refused.
func TestParseMonthDay_SlashIsOnlyAmbiguousWhenItReallyIs(t *testing.T) {
	t.Parallel()
	got, err := ParseMonthDay("30/09")
	require.NoError(t, err)
	assert.Equal(t, "09-30", got)

	got, err = ParseMonthDay("01/01")
	require.NoError(t, err, "the same number twice reads the same either way round")
	assert.Equal(t, "01-01", got)
}

func TestParseMonthDay_Refusals(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":         "empty",
		"   ":      "empty",
		"1231":     "single number",
		"45657":    "single number", // A date column somebody saved as serial numbers.
		"2026":     "year on its own",
		"13-13":    "neither of its two numbers can be a month",
		"02-30":    "not a date that exists",
		"Smarch 3": "not a number or a month name",
		"1-2-3-4":  "too many parts",
	}
	for input, want := range cases {
		_, err := ParseMonthDay(input)
		require.Error(t, err, input)
		assert.Contains(t, err.Error(), want, input)
	}
}

// Whatever this parser accepts, the deadline engine's own parser accepts —
// which is what makes an imported year end indistinguishable from a typed one.
func TestParseMonthDay_ProducesWhatTheEngineParses(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"12-31", "1 Jan", "29/02", "2026-06-30"} {
		value, err := ParseMonthDay(input)
		require.NoError(t, err, input)
		assert.Len(t, value, 5, input)
		assert.Equal(t, byte('-'), value[2], input)
	}
}
