package domain

import (
	"strconv"
	"strings"
)

// Source formats an uploaded file may turn out to be.
const (
	FormatCSV  = "csv"
	FormatXLSX = "xlsx"
)

// Row is one line of a source file. Number is the 1-based row number the
// customer sees in their own spreadsheet and it survives every skip this
// package makes — a blank line in the middle of a file shifts nothing, so row
// 37 in a message is row 37 in the file.
type Row struct {
	Number int      `json:"number"`
	Cells  []string `json:"cells"`
	// SourceWidth is how many values the file's own record carried, counted
	// where the file was parsed and before any trailing empty column was
	// trimmed away. It is the only surviving evidence that a row was SPLIT: a
	// cell holding "06-30;12-31" in a semicolon file becomes two values, and
	// once the trailing blanks are trimmed the row occupies no more columns
	// than the header does, so nothing about its shape gives the split away
	// afterwards. Zero where the format has no record width of its own — a
	// workbook's grid cannot split a cell, so there is nothing to compare.
	SourceWidth int `json:"sourceWidth,omitempty"`
}

// Cell returns the value at a 0-based column index, or "" when the row is
// shorter than that. Real exports ragged-end their rows; a missing cell is an
// empty one, not an error.
func (r Row) Cell(col int) string {
	if col < 0 || col >= len(r.Cells) {
		return ""
	}
	return r.Cells[col]
}

// IsBlank reports whether every cell of the row is empty.
func (r Row) IsBlank() bool {
	for _, c := range r.Cells {
		if c != "" {
			return false
		}
	}
	return true
}

// Sheet is one tab of a workbook, or the whole of a delimited file, in the
// format-agnostic shape the validator reads. The parsing half produces it; this
// half never learns what a zip, a delimiter or a byte-order mark is.
type Sheet struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Rows   []Row  `json:"rows"`
	// Hidden is a tab hidden in Excel. A hidden sheet is nearly always last
	// year's copy or a lookup table, and it is never what a customer means by
	// "import this workbook" — but its tab name matches just as well as a
	// visible one's, so without this the wrong one can win in silence.
	Hidden bool `json:"hidden,omitempty"`
	// Merges are the sheet's merged blocks. A merge holds its value in the
	// top-left cell only and leaves the rest of the block empty, which reads as
	// a value the customer cleared — so the blocks are carried here rather than
	// discarded, and the value is given back to the cells it covers.
	Merges []MergedRange `json:"merges,omitempty"`
}

// MergedRange is one merged block of a worksheet. Rows are the 1-based numbers
// the customer sees; columns are 0-based, the way Row.Cells is indexed.
type MergedRange struct {
	Ref      string `json:"ref"`
	FirstRow int    `json:"firstRow"`
	LastRow  int    `json:"lastRow"`
	FirstCol int    `json:"firstCol"`
	LastCol  int    `json:"lastCol"`
}

// SpansOneColumn reports a merge that runs down a single column — the way a
// register says "this value covers these rows", and the only shape whose value
// can be given back to the cells it covers without inventing anything.
func (m MergedRange) SpansOneColumn() bool {
	return m.FirstCol == m.LastCol && m.LastRow > m.FirstRow
}

// IsEmpty reports whether the sheet carries no value at all.
func (s Sheet) IsEmpty() bool {
	for _, r := range s.Rows {
		if !r.IsBlank() {
			return false
		}
	}
	return true
}

// File is one uploaded file: the format it turned out to be and its sheets in
// workbook order (a delimited file has exactly one). Notes record what the
// reader had to decide in order to read it at all — the delimiter it settled
// on, the encoding it decoded — so a customer can see the assumptions.
type File struct {
	Format string  `json:"format"`
	Sheets []Sheet `json:"sheets"`
	Notes  []Issue `json:"notes"`
}

// PickSheet chooses the sheet to import for a target: the one whose tab name
// says so ("Entities", "Tax Obligations", …), else the only sheet with
// anything in it. A workbook with several candidate sheets and no name match
// is refused rather than guessed — importing the wrong tab is silent and
// expensive. The returned issues carry the choice, or the refusal.
//
// Two rules make the choice something a customer can check rather than trust.
//
// A HIDDEN tab is not a candidate while any visible one is. Its tab name
// matches exactly as well as a visible one's, so a hidden "Entities" holding
// last year's register beats the visible "Working copy" the customer is
// actually maintaining — and every row of the answer then looks plausible,
// because stale data looks like data. Hidden sheets are still read when there
// is nothing else, because hiding the data tab is somebody's filing habit and
// refusing it would be refusing the customer; it is said out loud instead.
//
// And WHICH sheet was read is reported for every workbook, whether or not the
// choice was difficult. It is one line, and it is the only line in the report
// that can give away a wrong-tab import: every other line is about rows that
// are, in themselves, perfectly valid.
func (f *File) PickSheet(target Target) (Sheet, []Issue) {
	var named, filled, hiddenNamed, hiddenFilled []Sheet
	for _, s := range f.Sheets {
		if s.IsEmpty() {
			continue
		}
		if s.Hidden {
			hiddenFilled = append(hiddenFilled, s)
			if matchesSheetName(s.Name, target) {
				hiddenNamed = append(hiddenNamed, s)
			}
			continue
		}
		filled = append(filled, s)
		if matchesSheetName(s.Name, target) {
			named = append(named, s)
		}
	}

	switch {
	case len(named) == 1:
		return f.chose(named[0], "it is the tab named for "+target.Label(), hiddenNamed)
	case len(named) > 1:
		return Sheet{}, []Issue{FileError(ErrSheetAmbiguous(target, sheetNames(named)))}
	case len(filled) == 1:
		return f.chose(filled[0], "it is the only sheet in this workbook with data in it", hiddenNamed)
	case len(filled) > 1:
		return Sheet{}, []Issue{FileError(ErrSheetUnnamed(target, sheetNames(filled)))}
	}

	// Nothing visible holds anything: the workbook's data is on a hidden tab,
	// which is read rather than refused — and named, because a customer who did
	// not know it was hidden needs to be told which sheet the report is about.
	switch {
	case len(hiddenNamed) == 1:
		return f.chose(hiddenNamed[0], "it is hidden in Excel, and it is the tab named for "+target.Label(), nil)
	case len(hiddenNamed) > 1:
		return Sheet{}, []Issue{FileError(ErrSheetAmbiguous(target, sheetNames(hiddenNamed)))}
	case len(hiddenFilled) == 1:
		return f.chose(hiddenFilled[0],
			"it is hidden in Excel, and it is the only sheet in this workbook with data in it", nil)
	case len(hiddenFilled) > 1:
		return Sheet{}, []Issue{FileError(ErrSheetUnnamed(target, sheetNames(hiddenFilled)))}
	default:
		return Sheet{}, []Issue{FileError(ErrFileEmpty())}
	}
}

// chose records the tab a workbook was read from, and names any hidden tab that
// could have been meant instead. A delimited file has one sheet whose name is
// its own file name, so there is nothing there for a customer to check and
// nothing is said.
func (f *File) chose(sheet Sheet, because string, passedOver []Sheet) (Sheet, []Issue) {
	if f.Format != FormatXLSX {
		return sheet, nil
	}
	issues := []Issue{FileWarning(ErrSheetRead(sheet.Name, because))}
	if len(passedOver) > 0 {
		issues = append(issues, FileWarning(ErrHiddenSheetNotRead(sheetNames(passedOver))))
	}
	return sheet, issues
}

func sheetNames(sheets []Sheet) []string {
	out := make([]string, 0, len(sheets))
	for _, s := range sheets {
		out = append(out, s.Name)
	}
	return out
}

// ColumnLetter renders a 0-based column index the way a spreadsheet names it
// (0 → A, 25 → Z, 26 → AA), so an issue can be found by looking at the file.
func ColumnLetter(col int) string {
	if col < 0 {
		return ""
	}
	var sb strings.Builder
	for n := col; ; n = n/alphabetLen - 1 {
		sb.WriteByte(byte('A' + n%alphabetLen))
		if n < alphabetLen {
			break
		}
	}
	runes := []byte(sb.String())
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

const alphabetLen = 26

// CellRef is the spreadsheet address of a cell: a 0-based column index and a
// 1-based row number render as "D12".
func CellRef(col, row int) string {
	letter := ColumnLetter(col)
	if letter == "" || row <= 0 {
		return ""
	}
	return letter + strconv.Itoa(row)
}
