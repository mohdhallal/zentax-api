package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spellings a spreadsheet writes a whole number of days or months in, and
// the one that has two readings and therefore has neither.
func TestParseWholeCount_AcceptsWhatASpreadsheetWrites(t *testing.T) {
	t.Parallel()
	accepted := map[string]int{
		"0":    0,
		"10":   10,
		"366":  366,
		"2.0":  2,    // A formatted cell hands a whole number over with a decimal point.
		"2.00": 2,    // Two decimal places, the other common cell format.
		"007":  7,    // Padded by an export.
		"1000": 1000, // Out of range for a deadline, but a number, and refused by range.
	}
	for input, want := range accepted {
		got, ok := parseWholeCount(input)
		assert.True(t, ok, input)
		assert.Equal(t, want, got, input)
	}
}

// "1.000" is one thousand wherever the decimal separator is a comma, and one to
// three decimal places everywhere else. Read by a general number parser it
// silently becomes 1 — in the same file whose other German number, "10,0", is
// refused out loud on the row above. A value with two readings is refused,
// never guessed.
func TestParseWholeCount_RefusesEverySpellingWithTwoReadings(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"1.000",    // A thousands separator, the whole point of this test.
		"2.000",    // Indistinguishable in shape from the one above.
		"1,000",    // The other convention's thousands separator.
		"1.000,00", // And both at once.
		"10,0",     // A decimal comma.
		"1.5",      // Half a month is not a deadline.
		// Go's own float syntax, none of which a spreadsheet writes.
		"1_000", "0x10p0", "3E+01", "Inf", "NaN",
		"", "-1", "+5", "5 days",
	} {
		_, ok := parseWholeCount(input)
		assert.False(t, ok, input)
	}
}

// The same name in the two ways Unicode spells it is one key.
//
// "Müller GmbH" out of Windows Excel is a precomposed ü; the same name copied
// from a list edited on macOS is u followed by a combining diaeresis. They are
// identical on screen and different byte for byte, so a key that does not fold
// them lets one file create a second entity beside the one it meant to update —
// and nothing on the page then tells the twins apart.
func TestNormalizeKey_FoldsTheWaysUnicodeSpellsOneName(t *testing.T) {
	t.Parallel()
	// Built from code points rather than typed: a combining mark in source is a
	// character nobody can review, and two lines that must differ would look
	// identical in the diff that changes them.
	const (
		umlautU   = "\u00fc" // One precomposed character, as Windows Excel writes it.
		combining = "\u0308" // A combining diaeresis, as macOS leaves it.
	)
	var (
		nfc = "M" + umlautU + "ller GmbH"
		nfd = "Mu" + combining + "ller GmbH"
		mix = "  M" + strings.ToUpper(umlautU) + "LLER   gmbh   " // case and spacing, which the key already folded
	)
	require.NotEqual(t, nfc, nfd, "the fixtures must really be different bytes")
	assert.Equal(t, NormalizeKey(nfc), NormalizeKey(nfd))
	assert.Equal(t, NormalizeKey(nfc), NormalizeKey(mix))

	// A script where decomposition is ordinary rather than an accident of one
	// operating system: a Hangul syllable, precomposed and as its three jamo.
	assert.Equal(t, NormalizeKey("\ud55c"), NormalizeKey("\u1112\u1161\u11ab"))

	// And it still separates what a person would call two different entities.
	assert.NotEqual(t, NormalizeKey(nfc), NormalizeKey(nfc+" AG"))
	assert.NotEqual(t, NormalizeKey("Muller GmbH"), NormalizeKey(nfc),
		"an accent is part of the name; only the way it is spelled is folded")
}

// SameText is the change list's comparison: the same fold over the spellings of
// one string, and nothing else — a re-capitalised or re-spaced name is still an
// edit the customer asked for, and is reported and written as one.
func TestSameText_FoldsSpellingAndNothingElse(t *testing.T) {
	t.Parallel()
	nfc := "M\u00fcller GmbH"
	nfd := "Mu\u0308ller GmbH"
	assert.True(t, SameText(nfc, nfd))
	assert.True(t, SameText("", ""))
	assert.False(t, SameText(nfc, strings.ToUpper(nfc)), "case is a real edit")
	assert.False(t, SameText("M"+"\u00fc"+"ller  GmbH", nfc), "so is spacing")
	assert.False(t, SameText(nfc, "Muller GmbH"))
}

// The tokens a spreadsheet leaves in a cell whose formula did not resolve. None
// of them is ever a value, in any column.
func TestIsSpreadsheetError_KnowsTheTokensAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"#N/A", "#REF!", "#VALUE!", "#DIV/0!", "#NAME?", "#NUM!", "#NULL!",
		"#SPILL!", "#CALC!", "#GETTING_DATA",
		"#n/a", "#Ref!", // However the export cased it.
		"#NV", "#BEZUG!", "#WERT!", // What a German Excel writes into a CSV.
	} {
		assert.True(t, isSpreadsheetError(input), input)
	}

	// Matched whole, never as a substring: a legal name may carry a "#", and a
	// tax reference certainly may.
	for _, input := range []string{
		"", "Acme GmbH", "#1 Trading Ltd", "Unit #N/A House", "N/A", "NA", "#",
	} {
		assert.False(t, isSpreadsheetError(input), input)
	}
}
