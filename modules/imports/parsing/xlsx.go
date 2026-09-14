package parsing

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// isoDateLayout is how a date CELL is handed to the validating half. Reading
// the raw serial and re-rendering it ourselves is the only way to be sure what
// the date is: the formatted value a workbook shows depends on the number
// format, and "01/12/2026" shown to a European and to an American is two
// different days. An ISO date is nobody's local convention and cannot be
// misread.
const isoDateLayout = "2006-01-02"

// excelSerialMax is 9999-12-31 — beyond it a number is not a date, whatever it
// is formatted as.
const excelSerialMax = 2958465

// readWorkbook reads an .xlsx into sheets.
func (r *Reader) readWorkbook(data []byte) (*domain.File, error) {
	// What the archive says it unpacks to is settled first, from its table of
	// contents. Every bound after this one is measured on values that already
	// exist in memory, which is too late for a format that is dangerous while
	// it expands rather than while it is read.
	if err := r.boundExpansion(data); err != nil {
		return nil, err
	}

	book, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    r.limits.MaxUnzipBytes,
		UnzipXMLSizeLimit: r.limits.MaxPartBytes,
	})
	if err != nil {
		return nil, apperrors.NewValidation(errWorkbookUnreadable(err))
	}
	defer func() { _ = book.Close() }()

	conv := newDateConverter(book)
	out := &domain.File{Format: domain.FormatXLSX}

	for i, name := range book.GetSheetList() {
		if i >= r.limits.MaxSheets {
			break
		}
		grid, err := book.GetRows(name, excelize.Options{RawCellValue: true})
		if err != nil {
			out.Notes = append(out.Notes, domain.FileWarning(errSheetUnreadable(name, err)))
			continue
		}
		if err := r.boundGrid(name, grid); err != nil {
			return nil, err
		}

		conv.startSheet()
		rows := make([]domain.Row, 0, len(grid))
		for rowIdx, record := range grid {
			if len(record) > r.limits.MaxColumns {
				record = record[:r.limits.MaxColumns]
			}
			cells := make([]string, len(record))
			for col, raw := range record {
				cells[col] = conv.cell(name, col, rowIdx, raw)
			}
			rows = append(rows, domain.Row{Number: rowIdx + 1, Cells: cells})
		}
		if refs, total := conv.uncachedFormulas(); total > 0 {
			out.Notes = append(out.Notes, domain.FileError(errUncachedFormulas(name, refs, total)))
		}
		bounded, err := r.bound(trimEdges(rows))
		if err != nil {
			return nil, err
		}
		out.Sheets = append(out.Sheets, domain.Sheet{
			Name:   name,
			Format: domain.FormatXLSX,
			Rows:   bounded,
			Hidden: isHidden(book, name),
			Merges: r.mergedRanges(book, name),
		})
	}
	if len(out.Sheets) == 0 {
		return nil, apperrors.NewValidation("this workbook has no sheets in it.")
	}
	return out, nil
}

// isHidden reports a tab hidden in Excel — hidden or "very hidden", which is
// the same thing to everyone except a macro. GetSheetList hands over both
// exactly as it hands over the visible ones, so without asking, a hidden sheet
// competes for the import on equal terms with the sheet the customer is
// actually looking at. A sheet whose state cannot be read is treated as
// visible: the cost of that is a disclosed choice, and the cost of the opposite
// is a sheet that silently disappears.
func isHidden(book *excelize.File, name string) bool {
	visible, err := book.GetSheetVisible(name)
	if err != nil {
		return false
	}
	return !visible
}

// maxMergedRanges bounds the merged blocks one sheet is read for. A register
// merges a handful of cells; a file declaring thousands is not a register, and
// the blocks past the bound are simply not expanded — which is the behaviour
// the reader had for all of them until now.
const maxMergedRanges = 1024

// mergedRanges reads the sheet's merged blocks as coordinates. Only the
// references are asked for, never the values: a merged cell's value comes back
// FORMATTED, and a formatted date is the one thing this reader goes out of its
// way never to read — "01/12/2026" is two different days depending on who is
// looking. The value is taken from the converted grid instead.
func (r *Reader) mergedRanges(book *excelize.File, sheet string) []domain.MergedRange {
	merges, err := book.GetMergeCells(sheet, true)
	if err != nil {
		return nil
	}
	out := make([]domain.MergedRange, 0, len(merges))
	for i := range merges {
		if len(out) >= maxMergedRanges {
			break
		}
		firstCol, firstRow, err := excelize.CellNameToCoordinates(merges[i].GetStartAxis())
		if err != nil {
			continue
		}
		lastCol, lastRow, err := excelize.CellNameToCoordinates(merges[i].GetEndAxis())
		if err != nil {
			continue
		}
		if firstCol > lastCol {
			firstCol, lastCol = lastCol, firstCol
		}
		if firstRow > lastRow {
			firstRow, lastRow = lastRow, firstRow
		}
		if firstCol-1 >= r.limits.MaxColumns || lastRow > r.limits.MaxRows {
			continue
		}
		out = append(out, domain.MergedRange{
			Ref:      merges[i].GetStartAxis() + ":" + merges[i].GetEndAxis(),
			FirstRow: firstRow,
			LastRow:  lastRow,
			FirstCol: firstCol - 1,
			LastCol:  lastCol - 1,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// boundGrid refuses a sheet larger than an import may hold, before its cells
// are converted. The row cap is the same refusal the validating half makes, and
// the cell cap is the one the row and column caps together do not give: a sheet
// inside both can still be a quarter of a million cells, which is where the
// cost of a workbook actually lands.
func (r *Reader) boundGrid(sheet string, grid [][]string) error {
	if len(grid) > r.limits.MaxRows {
		return domain.ErrTooManyRows
	}
	if r.limits.MaxCells <= 0 {
		return nil
	}
	cells := 0
	for _, record := range grid {
		width := len(record)
		if width > r.limits.MaxColumns {
			width = r.limits.MaxColumns
		}
		cells += width
	}
	if cells > r.limits.MaxCells {
		return apperrors.NewValidation(errSheetTooManyCells(sheet, cells, r.limits.MaxCells))
	}
	return nil
}

// dateConverter turns the raw contents of a cell into something the validating
// half can read: a date cell into an ISO date, a number into the plainest
// spelling of itself, and anything else into its own text.
type dateConverter struct {
	book      *excelize.File
	date1904  bool
	dateStyle map[int]bool
	// padStyle caches, per style, whether the format displays a leading run of
	// zeros — see paddedNumber.
	padStyle map[int]bool

	// uncached collects, per sheet, the cells that hold a formula their workbook
	// never saved a result for — see noteUncachedFormula.
	uncached      []string
	uncachedTotal int
}

func newDateConverter(book *excelize.File) *dateConverter {
	conv := &dateConverter{book: book, dateStyle: map[int]bool{}, padStyle: map[int]bool{}}
	if props, err := book.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		conv.date1904 = *props.Date1904
	}
	return conv
}

// startSheet resets what is collected per sheet; the date-style cache is a
// property of the workbook and survives.
func (c *dateConverter) startSheet() {
	c.uncached, c.uncachedTotal = nil, 0
}

// uncachedFormulas is the sheet's uncalculated formula cells: the first few by
// reference, and how many there are altogether.
func (c *dateConverter) uncachedFormulas() ([]string, int) {
	return c.uncached, c.uncachedTotal
}

func (c *dateConverter) cell(sheet string, col, rowIdx int, raw string) string {
	value := domain.CleanCell(raw)
	if value == "" {
		c.noteUncachedFormula(sheet, col, rowIdx)
		return ""
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		// Text: a shared string, an inline string, a formula's cached result.
		return value
	}
	if c.isDateCell(sheet, col, rowIdx, number) {
		if when, convErr := excelize.ExcelDateToTime(number, c.date1904); convErr == nil {
			return when.Format(isoDateLayout)
		}
	}
	if shown, padded := c.paddedNumber(sheet, col, rowIdx, number, value); padded {
		return shown
	}
	return formatNumber(number, value)
}

// paddedNumber is the cell as the sheet SHOWS it, for the one numeric format
// whose display carries information the stored number does not: a leading run
// of zeros.
//
// A tax authority issues 0012345 and the customer types it into a cell
// formatted "0000000" — which is precisely how a spreadsheet is told that this
// column of digits is an identifier rather than a quantity. The workbook then
// stores 12345 with the zeros in the format, and reading the raw number drops
// two digits from a filing reference, silently, in a value nobody proofreads
// digit by digit. So when the format pads, the formatted value is the value.
//
// It is asked only of whole numbers under a CUSTOM format, and the style lookup
// is the one isDateCell already makes on the same cell, so this costs one more
// predicate on a style that is already in hand.
func (c *dateConverter) paddedNumber(sheet string, col, rowIdx int, number float64, raw string) (string, bool) {
	if number != float64(int64(number)) || strings.ContainsAny(raw, ".eE") {
		return "", false
	}
	ref, err := excelize.CoordinatesToCellName(col+1, rowIdx+1)
	if err != nil {
		return "", false
	}
	styleID, err := c.book.GetCellStyle(sheet, ref)
	if err != nil {
		return "", false
	}
	pads, seen := c.padStyle[styleID]
	if !seen {
		pads = c.styleZeroPads(styleID)
		c.padStyle[styleID] = pads
	}
	if !pads {
		return "", false
	}
	shown, err := c.book.GetCellValue(sheet, ref, excelize.Options{RawCellValue: false})
	if err != nil {
		return "", false
	}
	if shown = domain.CleanCell(shown); shown == "" {
		return "", false
	}
	return shown, true
}

func (c *dateConverter) styleZeroPads(styleID int) bool {
	style, err := c.book.GetStyle(styleID)
	if err != nil || style == nil || style.CustomNumFmt == nil {
		// None of the formats ECMA-376 reserves pads: the built-in numeric ones
		// are "0", "0.00", "#,##0" and their currency and percentage relatives,
		// whose minimum integer width is one digit. Padding is always a format
		// somebody wrote on purpose.
		return false
	}
	return zeroPaddingFormat(*style.CustomNumFmt)
}

// minPaddingZeros is how many zero placeholders make a format a PADDING one.
//
// Two, not one: "0.00" and "#,##0" each carry a single leading zero and are
// ordinary quantities — taking their displayed value would turn a filing offset
// of 10 days into "10.00" and a count of 1200 into "1,200". Two zeros in the
// integer part is a minimum width nobody asks for unless the zeros are meant to
// be seen.
const minPaddingZeros = 2

// zeroPaddingFormat reports whether a number format displays a leading run of
// zeros. It reads the positive section's INTEGER placeholders — the part before
// any decimal point, with quoted literals and bracketed colours already
// stripped — and counts the zeros, which is what sets the minimum digit width.
//
// Anything that is not a plain padded integer is not this: a date (letters), a
// percentage or a fraction (the displayed number is not the stored one), text.
// Those keep the raw value, which for them is the faithful reading.
func zeroPaddingFormat(code string) bool {
	section := stripFormatLiterals(code)
	if i := strings.IndexByte(section, ';'); i >= 0 {
		section = section[:i] // The positive section is the one a reference number is shown in.
	}
	if i := strings.IndexByte(section, '.'); i >= 0 {
		section = section[:i]
	}
	if strings.ContainsAny(section, "%/") {
		return false
	}
	for _, r := range section {
		if unicode.IsLetter(r) {
			return false // A date, "General", or a unit written without quotes.
		}
	}
	return strings.Count(section, "0") >= minPaddingZeros
}

// noteUncachedFormula records an empty cell that is not empty at all: it holds
// a formula whose result the workbook never saved.
//
// The grid is read raw, which is what makes a date cell readable as the serial
// under it rather than as whatever the number format shows. The same setting
// hands over a formula's CACHED result — and an empty string when there is no
// cache, which is what a workbook written by a generator or saved with
// "calculate on load" carries. Read as empty, such a cell is indistinguishable
// from one the customer cleared on purpose; and a blank cell in a column the
// file includes is a statement that clears the stored value. So it is looked up
// and reported rather than read.
//
// Only cells that came back empty are asked, and the sheet's used range is
// already bounded by the cell cap, so the lookup runs on the few cells that
// could possibly be one.
func (c *dateConverter) noteUncachedFormula(sheet string, col, rowIdx int) {
	ref, err := excelize.CoordinatesToCellName(col+1, rowIdx+1)
	if err != nil {
		return
	}
	formula, err := c.book.GetCellFormula(sheet, ref)
	if err != nil || formula == "" {
		return
	}
	c.uncachedTotal++
	if len(c.uncached) < namedFormulaCells {
		c.uncached = append(c.uncached, ref)
	}
}

// isDateCell asks the cell's number format whether the number under it is a
// date. Only numbers are asked, so the style lookup runs on the few cells that
// could possibly be dates rather than on the whole sheet.
func (c *dateConverter) isDateCell(sheet string, col, rowIdx int, number float64) bool {
	if number < 1 || number > excelSerialMax {
		return false
	}
	ref, err := excelize.CoordinatesToCellName(col+1, rowIdx+1)
	if err != nil {
		return false
	}
	styleID, err := c.book.GetCellStyle(sheet, ref)
	if err != nil {
		return false
	}
	if known, seen := c.dateStyle[styleID]; seen {
		return known
	}
	isDate := c.styleIsDate(styleID)
	c.dateStyle[styleID] = isDate
	return isDate
}

func (c *dateConverter) styleIsDate(styleID int) bool {
	style, err := c.book.GetStyle(styleID)
	if err != nil || style == nil {
		return false
	}
	if style.CustomNumFmt != nil {
		return looksLikeDateFormat(*style.CustomNumFmt)
	}
	return builtinDateNumFmts[style.NumFmt]
}

// builtinDateNumFmts are the number formats ECMA-376 reserves for dates. The
// time-only ones (18-21, 45-47) are deliberately absent: a clock time is not a
// date, and turning one into 1899-12-31 would invent a value.
var builtinDateNumFmts = func() map[int]bool {
	ids := map[int]bool{14: true, 15: true, 16: true, 17: true, 22: true}
	for id := 27; id <= 36; id++ { // East-Asian locale date formats.
		ids[id] = true
	}
	for id := 50; id <= 58; id++ { // The rest of the same block.
		ids[id] = true
	}
	return ids
}()

// looksLikeDateFormat reports whether a custom number format renders a date. A
// year or a day-of-month settles it, and so does a month NAME: a bare "m" is
// minutes as often as months, so on its own it settles nothing.
func looksLikeDateFormat(code string) bool {
	stripped := stripFormatLiterals(code)
	return strings.ContainsAny(stripped, "yYdD") || strings.Contains(strings.ToLower(stripped), "mmm")
}

// stripFormatLiterals removes the parts of a number format that are text rather
// than date tokens: quoted literals, escaped characters, and the bracketed
// colour and locale sections.
func stripFormatLiterals(code string) string {
	var sb strings.Builder
	sb.Grow(len(code))
	inQuote, inBracket, escaped := false, false, false
	for _, r := range code {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
		case r == '[':
			inBracket = true
		case r == ']':
			inBracket = false
		case inBracket:
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// formatNumber writes a number back the plainest way. A workbook stores a tax
// reference number that was typed as a number in whatever spelling its writer
// chose, and "1.2345678901e+10" is not a tax reference number anybody can read.
func formatNumber(number float64, raw string) string {
	if number == float64(int64(number)) && !strings.ContainsAny(raw, "eE") {
		return raw
	}
	if number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}
