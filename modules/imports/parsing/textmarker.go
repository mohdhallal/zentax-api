package parsing

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// THE SPREADSHEET TEXT MARKER — one contract with two halves, and this is the
// reading half.
//
// A value that begins with "=", "+", "-", "@", a tab or a carriage return is
// read by Excel, Google Sheets and LibreOffice as the start of a FORMULA rather
// than as text, and a formula in a downloaded file executes on the machine of
// whoever opens it — an auditor's, a client's. So every file the product hands
// out neutralises such a value with the spreadsheet's own text marker, one
// leading apostrophe. That is the writing half and it lives in one place:
// zentax-ui/client/src/lib/export-cells.ts (needsFormulaGuard, guardFormula),
// which every CSV and .xls download goes through.
//
// The marker is FILE SYNTAX, not part of the value. A spreadsheet consumes it
// on the way in; a plain CSV reader does not — so until this file existed the
// product could not read its own export back. `+41 Kommunikations AG` went out
// as `'+41 Kommunikations AG` and came back as that, which is a different name
// and therefore a different natural key: a customer who exported their
// register, edited it and uploaded it again got a second entity under a
// corrupted name, with no file issue and no row issue to show for it.
//
// The two halves are exact inverses, and neither is allowed to move alone:
//
//	WRITING marks a value when it would be read as a formula — a formula lead,
//	more than one character, and not a plain number ("-500" and "-1,234.50" are
//	numbers to every reader and must stay arithmetic) — and ALSO when the value
//	already carries a marker by that same rule. The second case is what keeps
//	the encoding reversible: a company genuinely named `'+41 Ltd` goes out as
//	`''+41 Ltd`.
//
//	READING takes exactly one leading apostrophe off a cell whose remainder
//	satisfies either case, and says which cells it did that to.
//
// So the round trip closes: export any value, read the file back through this
// package, and the value is the one that went out. It is pinned on both sides —
// TestRead_TheProductsOwnExportReadsBackAsItself here, and "the round trip the
// import completes" in export-cells.test.ts.
//
// TWO THINGS THIS DELIBERATELY DOES NOT DO.
//
// It never touches a workbook cell. In OOXML the marker is not characters at
// all — it is the `quotePrefix` attribute of the cell's format — so the library
// hands the value over without it and there is nothing to take off. Running
// this rule over .xlsx would only corrupt names that genuinely begin with an
// apostrophe, so it is called from readDelimited alone.
//
// It never strips an apostrophe standing in front of anything that is not a
// formula lead. `'0012345`, the way a person marks a tax reference so its
// leading zero survives, and `'t Veld Beheer B.V.`, a Dutch legal name, both
// come through exactly as written. The first is the known cost and it is
// deliberate: nothing in a file distinguishes a marker somebody typed from an
// apostrophe that is part of the name, so the line is drawn exactly where the
// writing half draws it and nowhere else.

// textMarker is the apostrophe a spreadsheet reads as "this cell is text".
const textMarker = "'"

// formulaLeads are the characters a spreadsheet reads as the start of an
// expression: the four formula leads, and the two control characters Excel
// strips before it looks at the lead. The mirror of FORMULA_LEAD.
const formulaLeads = "=+-@\t\r"

// numberLiteral is a value every reader parses as a number rather than as an
// expression — an optional sign, digits (plain or grouped in threes), an
// optional fraction, an optional exponent. The writing half leaves these
// unmarked, so this half must not unmark them. The mirror of NUMBER_LITERAL,
// character for character; \d is ASCII digits in RE2 and in JavaScript alike.
var numberLiteral = regexp.MustCompile(`^[+-]?(\d{1,3}(,\d{3})+|\d+)(\.\d+)?([eE][+-]?\d+)?$`)

// needsTextMarker reports a value the writing half marks because a spreadsheet
// would otherwise evaluate it. The mirror of needsFormulaGuard.
//
// The length test counts bytes where the original counts characters, and the
// two mean the same thing here: a formula lead is one ASCII byte, so "at least
// two bytes" and "at least two characters" differ on no value that starts with
// one. A lone trigger character is not marked by either — "-" is this product's
// own empty-cell mark, and one character cannot form an expression.
func needsTextMarker(value string) bool {
	if len(value) < 2 {
		return false
	}
	if !strings.ContainsRune(formulaLeads, rune(value[0])) {
		return false
	}
	return !numberLiteral.MatchString(value)
}

// carriesTextMarker reports a value written in the marked form: one apostrophe
// in front of something the writing half would have marked — which includes
// another marked form. Looping rather than stopping at the first apostrophe is
// what makes the pair reversible for a value that genuinely begins with one.
func carriesTextMarker(value string) bool {
	rest, ok := strings.CutPrefix(value, textMarker)
	if !ok {
		return false
	}
	for {
		if needsTextMarker(rest) {
			return true
		}
		next, ok := strings.CutPrefix(rest, textMarker)
		if !ok {
			return false
		}
		rest = next
	}
}

// stripTextMarkers takes the marker off every cell of a delimited file that
// carries one, in place, and returns the remark naming the cells it changed —
// empty when there were none.
//
// Nothing here happens quietly. Taking the marker off is a reading decision of
// exactly the kind this package already reports out loud — which separator the
// file turned out to use, which encoding it turned out to be in — and it is
// reported the same way, naming the cell and the value it was read as, so a
// customer whose apostrophe was meant literally can see it and say so.
func stripTextMarkers(rows []domain.Row) string {
	var named []markedCell
	total := 0
	for i := range rows {
		for col, cell := range rows[i].Cells {
			if !carriesTextMarker(cell) {
				continue
			}
			value := strings.TrimPrefix(cell, textMarker)
			rows[i].Cells[col] = value
			total++
			if len(named) < namedMarkedCells {
				named = append(named, markedCell{ref: domain.CellRef(col, rows[i].Number), value: value})
			}
		}
	}
	if total == 0 {
		return ""
	}
	return errTextMarkerRemoved(named, total)
}

// markedCell is one cell the marker came off, named the way the customer can
// find it.
type markedCell struct {
	ref   string
	value string
}

// namedMarkedCells is how many cells the remark quotes before it counts the
// rest. A register exported from this product can carry the marker on hundreds
// of rows; three is enough to recognise what happened.
const namedMarkedCells = 3

// errTextMarkerRemoved records the marker coming off. It lives beside the rule
// rather than in messages.go because the message and the rule are the same
// contract, and a wording that drifts from the rule is worse than no wording.
func errTextMarkerRemoved(named []markedCell, total int) string {
	readings := make([]string, 0, len(named)+1)
	for i, cell := range named {
		if i == 0 {
			readings = append(readings, cell.ref+" was read as "+quoteValue(cell.value))
			continue
		}
		readings = append(readings, cell.ref+" as "+quoteValue(cell.value))
	}
	if total > len(named) {
		readings = append(readings, strconv.Itoa(total-len(named))+" more")
	}

	subject := named[0].ref + " begins"
	if total > 1 {
		subject = strconv.Itoa(total) + " cells begin"
	}
	return subject + " with an apostrophe in front of a formula character — the text marker a spreadsheet," +
		" and this product's own export, writes so that a value starting with \"=\", \"+\", \"-\" or \"@\" is" +
		" not read as a formula. The marker is not part of the value and was not imported: " +
		strings.Join(readings, ", ") + ". If the apostrophe is part of the value itself, write it twice" +
		" in your file and upload the file again."
}
