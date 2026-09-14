package domain

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// The invisible and non-breaking characters real exports carry, named because a
// literal one in source is a character nobody can review.
const (
	runeBOM                = '\uFEFF'
	runeZeroWidthSpace     = '\u200B'
	runeZeroWidthNonJoiner = '\u200C'
	runeZeroWidthJoiner    = '\u200D'
	runeWordJoiner         = '\u2060'
	runeNoBreakSpace       = '\u00A0'
	runeFigureSpace        = '\u2007'
	runeNarrowNoBreakSpace = '\u202F'
)

// CleanCell normalises one raw cell the way a person reading the file would:
// the byte-order mark an exporter leaves on the very first cell, the
// non-breaking and zero-width spaces a copy-and-paste drags in, and the padding
// a fixed-width export adds all go. The inside of the value is left exactly as
// written — a legal name is not this function's to tidy.
func CleanCell(s string) string {
	if s == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case runeBOM, runeZeroWidthSpace, runeZeroWidthNonJoiner, runeZeroWidthJoiner, runeWordJoiner:
			// Invisible: dropped.
		case runeNoBreakSpace, runeFigureSpace, runeNarrowNoBreakSpace:
			sb.WriteRune(' ')
		default:
			sb.WriteRune(r)
		}
	}
	return strings.TrimFunc(sb.String(), unicode.IsSpace)
}

// NormalizeKey folds a human-written name to the form an import matches
// existing records on: outer space gone, runs of inner whitespace collapsed to
// one, case folded, and the several ways Unicode can spell one word folded to
// NFC. "Acme  GmbH " and "ACME GmbH" are the same entity; "Acme GmbH" and "Acme
// GmbH AG" are not.
//
// The NFC fold is not cosmetic. "Müller GmbH" written by Windows Excel is one
// precomposed ü; the same name copied out of a list edited on macOS is a plain
// u followed by a combining diaeresis. The two are identical on screen and
// different byte for byte, so without this fold one file creates a second
// entity beside the one it meant to update — and neither the customer nor the
// product can tell the twins apart afterwards, because the only thing that
// distinguishes them is invisible.
//
// It is folded LAST, after the lower-casing, because lower-casing can itself
// produce a decomposed sequence (a capital I with a dot above lower-cases to
// i + U+0307), and a key that depends on which spelling arrived is not a key.
func NormalizeKey(s string) string {
	return norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(CleanCell(s)), " ")))
}

// SameText reports whether two values are the same text once those same Unicode
// spellings are folded together.
//
// It is NOT NormalizeKey: case and inner spacing still tell two values apart,
// because correcting either is a real edit a customer can ask an import to
// make. It exists for the change list, where a file written on a Mac and a
// record created in a browser would otherwise report a change whose two sides
// are character-for-character identical on screen — and rewrite the stored
// value to say the same thing.
func SameText(a, b string) bool {
	return a == b || norm.NFC.String(a) == norm.NFC.String(b)
}

// errBlank is returned by the value readers when there is nothing to read; the
// row readers turn it into "required", or into a default.
var errBlank = errors.New("blank")

// labelFor is the heading a message calls a field by: the one the customer
// actually wrote, and failing that the one this import's own template writes.
// Quoting our field name at somebody looking at their own spreadsheet is how a
// report stops being readable.
func labelFor(b *Binding, field string) string {
	if b != nil {
		if header := b.Header(field); header != "" {
			return header
		}
	}
	if header, ok := canonicalHeaders[field]; ok {
		return header
	}
	return field
}

// canonicalHeaders is every field's own template heading. Field names do not
// repeat between the two targets, so one map serves both.
var canonicalHeaders = func() map[string]string {
	out := map[string]string{}
	for _, target := range []Target{TargetEntities, TargetObligations} {
		for _, f := range Fields(target) {
			if len(f.Aliases) > 0 {
				out[f.Name] = f.Aliases[0]
			}
		}
	}
	return out
}()

// spreadsheetErrors are the tokens a spreadsheet writes into a cell whose
// formula did not resolve. None of them is ever a value: a country is not
// "#N/A" and an entity is not "#REF!". They reach an import constantly, because
// a register assembled with VLOOKUPs against another sheet is exactly the file
// a customer brings to a pilot, and a half-finished lookup column leaves these
// behind in the cells it could not fill.
//
// They are matched whole and case-insensitively, never as a substring: a legal
// name may legitimately contain a "#".
//
// The English spellings are what an .xlsx stores whatever the user's language
// is, and the German ones are what a German Excel writes into a CSV export —
// the same file this reader already decodes from Windows-1252.
var spreadsheetErrors = map[string]bool{
	"#n/a": true, "#ref!": true, "#value!": true, "#div/0!": true,
	"#name?": true, "#num!": true, "#null!": true, "#spill!": true,
	"#calc!": true, "#field!": true, "#blocked!": true, "#connect!": true,
	"#unknown!": true, "#getting_data": true, "#external!": true,
	"#nv": true, "#bezug!": true, "#wert!": true, "#zahl!": true, "#leer!": true,
}

// isSpreadsheetError reports whether a cleaned cell is a spreadsheet's own
// error text rather than a value.
func isSpreadsheetError(value string) bool {
	return spreadsheetErrors[strings.ToLower(value)]
}

// errSpreadsheetError refuses one. It is a row-level error rather than a
// warning because the alternative is worse than a refusal in every direction:
// "#N/A" as a name becomes the entity's natural key, so the repaired row
// creates a SECOND entity on the next import and the broken one stays; "#REF!"
// as a country goes into the record the deadline engine reads.
//
// It lives beside the readers that raise it rather than in error_messages.go
// because it is about the shape of a CELL, which is the only thing these
// functions know.
func errSpreadsheetError(label, value string) string {
	return quote(value) + " in " + quote(label) + " is a spreadsheet's own error text, not a value:" +
		" the formula behind that cell did not resolve. Repair the formula in your file, or type the" +
		" value in over it, and upload the file again."
}

// readText reads a cell as free text and enforces the single-record route's own
// length bound. maxLen of 0 means unbounded.
func readText(b *Binding, row Row, field string, minLen, maxLen int) (string, error) {
	value := CleanCell(row.Cell(b.IndexOrMissing(field)))
	if value == "" {
		return "", errBlank
	}
	if isSpreadsheetError(value) {
		return "", errors.New(errSpreadsheetError(labelFor(b, field), value))
	}
	if runes := len([]rune(value)); runes < minLen || (maxLen > 0 && runes > maxLen) {
		return "", errors.New(ErrLength(labelFor(b, field), minLen, maxLen))
	}
	return value, nil
}

// readEnum reads a cell as one of a vocabulary, accepting the spellings people
// actually write and refusing anything else with the whole list of what is
// allowed — a customer fixing a file should never have to guess the spelling.
func readEnum(b *Binding, row Row, field string, vocab vocabulary) (string, error) {
	raw := CleanCell(row.Cell(b.IndexOrMissing(field)))
	if raw == "" {
		return "", errBlank
	}
	if isSpreadsheetError(raw) {
		return "", errors.New(errSpreadsheetError(labelFor(b, field), raw))
	}
	if value, ok := vocab.lookup(raw); ok {
		return value, nil
	}
	return "", errors.New(ErrNotInVocabulary(labelFor(b, field), raw, vocab.allowed))
}

// readCount reads a cell as a whole non-negative number of months or days and
// enforces the bound the deadline rule's own validate tag carries.
func readCount(b *Binding, row Row, field string, maxValue int) (int, error) {
	raw := CleanCell(row.Cell(b.IndexOrMissing(field)))
	if raw == "" {
		return 0, errBlank
	}
	if isSpreadsheetError(raw) {
		return 0, errors.New(errSpreadsheetError(labelFor(b, field), raw))
	}
	n, ok := parseWholeCount(strings.TrimPrefix(raw, "+"))
	if !ok {
		return 0, errors.New(ErrNotAWholeNumber(labelFor(b, field), countUnit(field), raw))
	}
	if n < 0 || n > maxValue {
		return 0, errors.New(ErrOutOfRange(labelFor(b, field), countUnit(field), raw, 0, maxValue))
	}
	return n, nil
}

// parseWholeCount reads the spellings a spreadsheet writes a whole number in: a
// plain "2", and the "2.0" or "2.00" a formatted cell hands over.
//
// It refuses everything else, and the value it exists to refuse is "1.000" —
// which is one thousand wherever the decimal separator is a comma, and one
// written to three decimal places everywhere else. Handing that to a general
// number parser reads it as 1, silently, in a file whose OTHER German number
// ("10,0") is refused loudly on the row above; the customer gets a mixed answer
// and no reason to doubt the half that was quietly changed. Three digits after
// the point is exactly the width of a thousands group, so the line is drawn
// there: a value with two readings is refused, never guessed — the same rule
// the month-day reader applies to "05/04".
//
// Working from the digits rather than from ParseFloat also closes the spellings
// Go's float syntax accepts and no spreadsheet writes: "1_000", "0x10p0",
// "3E+01", "Inf".
func parseWholeCount(s string) (int, bool) {
	whole, fraction, hasPoint := strings.Cut(s, ".")
	if !isDigits(whole) {
		return 0, false
	}
	if hasPoint {
		const maxFractionDigits = 2 // Three is how a thousands separator is written.
		if len(fraction) == 0 || len(fraction) > maxFractionDigits || !isDigits(fraction) {
			return 0, false
		}
		if strings.Trim(fraction, "0") != "" {
			return 0, false // Half a month is not a deadline.
		}
	}
	n, err := strconv.Atoi(whole)
	if err != nil {
		// Too many digits to be an int is still a number, and the range check
		// is the honest refusal for it.
		return maxCountOverflow, true
	}
	return n, true
}

// maxCountOverflow stands for a number too long to hold, so the caller's range
// check refuses it by range rather than by shape.
const maxCountOverflow = 1 << 30

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// readMonthDay reads a cell as a single month-and-day value.
func readMonthDay(b *Binding, row Row, field string) (string, error) {
	raw := CleanCell(row.Cell(b.IndexOrMissing(field)))
	if raw == "" {
		return "", errBlank
	}
	if isSpreadsheetError(raw) {
		return "", errors.New(errSpreadsheetError(labelFor(b, field), raw))
	}
	value, err := ParseMonthDay(raw)
	if err != nil {
		return "", errors.New(ErrNotAMonthDay(labelFor(b, field), raw, err))
	}
	return value, nil
}

// readMonthDayList reads a cell holding several month-and-day values. Real
// files separate them with a semicolon, a comma, a pipe or a line break inside
// the cell; all four are accepted, and an empty entry between two separators is
// dropped rather than refused.
func readMonthDayList(b *Binding, row Row, field string) ([]string, error) {
	raw := CleanCell(row.Cell(b.IndexOrMissing(field)))
	if raw == "" {
		return nil, errBlank
	}
	if isSpreadsheetError(raw) {
		return nil, errors.New(errSpreadsheetError(labelFor(b, field), raw))
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == ',' || r == '|' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = CleanCell(part)
		if part == "" {
			continue
		}
		value, err := ParseMonthDay(part)
		if err != nil {
			return nil, errors.New(ErrNotAMonthDay(labelFor(b, field), part, err))
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, errBlank
	}
	return out, nil
}

// ptr returns a pointer to a non-empty string and nil for an empty one — the
// "not provided" the create inputs distinguish from a blank value.
func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
