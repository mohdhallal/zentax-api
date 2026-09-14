package parsing

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// This file holds the two checks that prove a file was read WHOLE. Everything
// else in this package is about reading a file generously; these two are about
// the one kind of generosity that cannot be allowed — quietly reading less than
// the customer sent. A file that parses to fewer rows than it holds, or to rows
// with values nothing will ever look at, must never be reported as clean.

// checkQuoting proves the delimited parse covered the file.
//
// encoding/csv is read with LazyQuotes on, which is right: a quotation mark
// inside an unquoted value is an inch mark, a nickname, a height, and refusing
// a register over one would be refusing the customer. But the same setting has
// a second effect that is not forgiveness at all — a value that BEGINS with a
// quotation mark and never closes it swallows every following line into itself,
// silently, until the next quotation mark or the end of the file.
//
// So a lone quotation mark is read as content, and said to have been; a
// quotation mark that ate lines is refused, naming the row, the cell and the
// lines. The two are told apart by asking whether the span a record covers is
// well-formed CSV on its own: a genuine multi-line value (an address cell with
// a line break in it) parses strictly as one record, and a runaway quote does
// not.
func checkQuoting(text string, delimiter rune, rows []domain.Row) (note string, err error) {
	if !strings.ContainsRune(text, '"') {
		return "", nil
	}
	starts := lineStarts(text)
	if swallows := findSwallows(text, delimiter, rows, starts); len(swallows) > 0 {
		first := swallows[0]
		return "", apperrors.NewValidation(errQuoteSwallowedLines(
			first.startLine, first.cellRef, first.firstLost, first.lastLost, first.lostCount, len(swallows)-1))
	}
	if parseErr := strictParseError(text, delimiter); parseErr != nil {
		return errQuoteInsideAValue(parseErr.Line, parseErr.Column), nil
	}
	return "", nil
}

// swallowedSpan is one record that ate lines which had something on them.
type swallowedSpan struct {
	startLine int
	cellRef   string
	firstLost int
	lastLost  int
	lostCount int
}

// findSwallows collects every record that covers a line with content on it and
// is not well-formed CSV on its own.
//
// Comparing consecutive record line numbers is not enough by itself: when the
// runaway quote reaches the end of the file the record numbers stay perfectly
// consecutive and the loss is entirely off the end, so the last record is
// measured against the file's own line count.
//
// All of them are collected rather than the first, because this module's whole
// bargain with a customer is that one upload reports everything wrong with a
// file. Only the first is described in full, though: after a runaway quote the
// records below it start wherever the quote happened to close, so the second
// one is worth counting and not worth explaining.
func findSwallows(text string, delimiter rune, rows []domain.Row, starts []int) []swallowedSpan {
	totalLines := len(starts)
	var found []swallowedSpan
	for i, row := range rows {
		next := totalLines + 1
		if i+1 < len(rows) {
			next = rows[i+1].Number
		}
		first, last, count := contentLinesBetween(text, starts, row.Number+1, next-1)
		if count == 0 {
			continue
		}
		if spanIsOneRecord(text, starts, delimiter, row.Number, next, len(row.Cells)) {
			continue // A quoted value with a line break in it: legitimate, and read correctly.
		}
		found = append(found, swallowedSpan{
			startLine: row.Number,
			cellRef:   domain.CellRef(firstCellWithLineBreak(row), row.Number),
			firstLost: first,
			lastLost:  last,
			lostCount: count,
		})
	}
	return found
}

// contentLinesBetween reports the lines in [from, to] that are not blank —
// blank ones are what encoding/csv skips between records, and skipping them is
// not swallowing them.
func contentLinesBetween(text string, starts []int, from, to int) (first, last, count int) {
	if to > len(starts) {
		to = len(starts)
	}
	for line := from; line <= to; line++ {
		if strings.TrimSpace(lineAt(text, starts, line)) == "" {
			continue
		}
		if count == 0 {
			first = line
		}
		last = line
		count++
	}
	return first, last, count
}

// spanIsOneRecord reports whether the text a record covers is, on its own, one
// well-formed CSV record of the same width. That is what separates a value with
// a line break inside it from a quote that ran away.
func spanIsOneRecord(text string, starts []int, delimiter rune, from, next, width int) bool {
	span := text[offsetOfLine(text, starts, from):offsetOfLine(text, starts, next)]
	reader := csv.NewReader(strings.NewReader(span))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = false

	record, err := reader.Read()
	if err != nil || len(record) != width {
		return false
	}
	_, err = reader.Read()
	return errors.Is(err, io.EOF)
}

// firstCellWithLineBreak is the column the runaway quote opened in: it is the
// one cell of the record that ended up holding a line break.
func firstCellWithLineBreak(row domain.Row) int {
	for col, cell := range row.Cells {
		if strings.ContainsAny(cell, "\n\r") {
			return col
		}
	}
	return len(row.Cells) - 1
}

// strictParseError is the first quoting complaint encoding/csv makes when it is
// NOT being lazy, which is how a quotation mark read as content is located well
// enough to be named.
func strictParseError(text string, delimiter rune) *csv.ParseError {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = false
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			return parseErr
		}
		if err != nil {
			return nil
		}
	}
}

// lineStarts indexes the file by physical line, so a record's span can be cut
// out of it and a line can be asked whether it holds anything.
func lineStarts(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' && i+1 < len(text) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineAt(text string, starts []int, line int) string {
	if line < 1 || line > len(starts) {
		return ""
	}
	end := len(text)
	if line < len(starts) {
		end = starts[line]
	}
	return text[starts[line-1]:end]
}

func offsetOfLine(text string, starts []int, line int) int {
	if line < 1 {
		return 0
	}
	if line > len(starts) {
		return len(text)
	}
	return starts[line-1]
}

// wideRow is one data row carrying values past the last column heading.
type wideRow struct {
	number          int
	width           int
	firstExtraRef   string
	firstExtraValue string
}

// checkRowWidth refuses rows wider than the header row.
//
// A short row is forgiven everywhere in this package — a real export
// ragged-ends its rows and a missing cell is an empty one. A LONG row is the
// opposite: it carries values in columns the header never named, which no field
// is bound to and nothing will ever read, so importing the row means importing
// something other than what it says.
//
// The way this happens in practice is not a stray note to the right of the
// table. It is a cell holding several values separated by a semicolon — which
// is exactly how this import's own template asks for several filing dates, and
// exactly the character Excel separates columns with wherever the decimal
// separator is a comma. Unquoted, "07-31;01-31" is two columns, the January
// return is never scheduled, and the only thing the report would otherwise say
// is a warning about the number of dates.
func checkRowWidth(sheet domain.Sheet, headerRow int) *domain.Issue {
	headerWidth := 0
	for _, row := range sheet.Rows {
		if row.Number == headerRow {
			headerWidth = significantWidth(row)
			break
		}
	}
	if headerWidth == 0 {
		return nil
	}

	var wide []wideRow
	for _, row := range sheet.Rows {
		if row.Number <= headerRow {
			continue
		}
		width := significantWidth(row)
		if width <= headerWidth {
			continue
		}
		extra := firstValueAtOrAfter(row, headerWidth)
		wide = append(wide, wideRow{
			number:          row.Number,
			width:           width,
			firstExtraRef:   domain.CellRef(extra, row.Number),
			firstExtraValue: row.Cell(extra),
		})
	}
	if len(wide) == 0 {
		return nil
	}
	issue := domain.FileError(errRowsWiderThanTheHeader(sheet.Format, headerRow, headerWidth, wide))
	return &issue
}

// shiftedRow is one data row that the file split into more values than the
// header names, without any of them landing past the last heading.
type shiftedRow struct {
	number    int
	width     int
	lastRef   string
	lastValue string
	heading   string
}

// checkRowShift refuses rows whose values landed under the wrong headings.
//
// This is the quiet half of the same defect checkRowWidth catches loudly, and
// it is the worse half. A cell holding "06-30;12-31" in a semicolon file — the
// spelling this import's own guide asks for, in the separator Excel uses across
// most of Europe — becomes two values, and every value after it moves one
// column to the right. When the columns at the end of the row are blank, which
// on a sixteen-column template they usually are, the row still OCCUPIES no more
// columns than the header: nothing about the shape of the table gives it away,
// checkRowWidth sees a row no wider than the header, and the report is clean.
//
// What survives is the count of values the file's own record carried. A record
// with more values in it than the header row has headings is a row nobody can
// read correctly: the filing date read as a payment date, the legal name read
// as a country. Every value in such a row is individually valid, which is why
// it must be a refusal — there is nothing further down the line that could
// notice.
//
// The same shape is also what a row ending in a stray separator produces, and
// the two cannot be told apart from the file: both are "this row holds more
// values than the table has columns". The message says so and gives the remedy
// for each, rather than picking one and being quietly wrong about the other.
func checkRowShift(sheet domain.Sheet, headerRow int) *domain.Issue {
	header, ok := rowNumbered(sheet, headerRow)
	if !ok || header.SourceWidth == 0 {
		return nil // A workbook's grid has no record width: a cell cannot split.
	}
	headerWidth := significantWidth(header)

	var shifted []shiftedRow
	for _, row := range sheet.Rows {
		if row.Number <= headerRow || row.SourceWidth <= header.SourceWidth {
			continue
		}
		if significantWidth(row) > headerWidth {
			continue // Loud enough on its own: checkRowWidth names the value.
		}
		last := lastValueColumn(row)
		if last < 0 {
			continue // Nothing but separators: no value landed anywhere.
		}
		shifted = append(shifted, shiftedRow{
			number:    row.Number,
			width:     row.SourceWidth,
			lastRef:   domain.CellRef(last, row.Number),
			lastValue: row.Cell(last),
			heading:   domain.CleanCell(header.Cell(last)),
		})
	}
	if len(shifted) == 0 {
		return nil
	}
	issue := domain.FileError(errRowValuesShifted(headerRow, header.SourceWidth, shifted))
	return &issue
}

// rowNumbered finds a row by the number the customer sees.
func rowNumbered(sheet domain.Sheet, number int) (domain.Row, bool) {
	for _, row := range sheet.Rows {
		if row.Number == number {
			return row, true
		}
	}
	return domain.Row{}, false
}

// lastValueColumn is the right-most column of a row that holds anything. In a
// shifted row it is the value that moved furthest from the heading it belongs
// to, and naming it beside the heading it was read under is what turns a count
// of values into something the customer can recognise in their own file.
func lastValueColumn(row domain.Row) int {
	for col := len(row.Cells) - 1; col >= 0; col-- {
		if row.Cells[col] != "" {
			return col
		}
	}
	return -1
}

// controlCharacterLimit is how many offending cells are named before the rest
// are counted: one is enough to find the file's problem, a few prove it is the
// whole file rather than one stray paste.
const controlCharacterLimit = 3

// checkControlCharacters refuses a file whose values carry characters that are
// not text.
//
// The one that matters is NUL. It is what a UTF-16 file read as if it were
// UTF-8 puts between every pair of characters, and it survives everything that
// tidies a cell: it is not a space, so it is not trimmed, and it is not a
// letter or a digit, so a header still normalises and still binds. The file
// then looks read. Postgres settles it much later and much worse — a NUL is not
// storable in text or in jsonb, so the upload dies on a database error that
// says nothing about the file.
//
// So it is refused here, by cell, in the words of the file it came from. The
// other C0 controls go with it: a bell or a form feed in a tax register is not
// a value anybody typed. Tab, carriage return and newline are exempt — a quoted
// address cell legitimately holds all three.
func checkControlCharacters(sheet domain.Sheet) error {
	var named []string
	total := 0
	var first string
	for _, row := range sheet.Rows {
		for col, cell := range row.Cells {
			if !hasControlCharacter(cell) {
				continue
			}
			total++
			if len(named) < controlCharacterLimit {
				named = append(named, domain.CellRef(col, row.Number))
			}
			if first == "" {
				first = cell
			}
		}
	}
	if total == 0 {
		return nil
	}
	return apperrors.NewValidation(errControlCharactersInValues(named, total, first))
}

func hasControlCharacter(cell string) bool {
	for _, r := range cell {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}

// expandMergedValues gives a merged block's value back to the cells it covers,
// for the one shape where that is reading the file rather than inventing: a
// merge that runs down a single column. That is what a group register means by
// merging — "this value covers these rows" — and what the reader saw instead
// was the value in the first row and an empty cell in every other, which is not
// a missing value but a statement that the field is empty.
//
// A merge that runs ACROSS columns is left exactly as it is and reported
// instead (mergedColumnSpans). Filling it would put one value under headings
// that mean different things: a note merged across a data row would become an
// entity's name AND its country, and a heading merged across two columns would
// claim the same field twice and refuse the file. The honest answer there is to
// say what was not read.
//
// Only blocks that lie fully BELOW the header row are touched. A heading merged
// down two rows is how a two-level header is written, and filling it would put
// column headings into the first data row.
func expandMergedValues(sheet *domain.Sheet, headerRow int) {
	if len(sheet.Merges) == 0 {
		return
	}
	// Indexed once: a sheet is missing the rows that were entirely empty, so a
	// row number is not an index, and looking one up per covered cell would be
	// quadratic on a workbook that merges a whole column.
	at := make(map[int]int, len(sheet.Rows))
	for i, row := range sheet.Rows {
		at[row.Number] = i
	}

	for _, merge := range sheet.Merges {
		if !merge.SpansOneColumn() || merge.FirstRow <= headerRow {
			continue
		}
		from, ok := at[merge.FirstRow]
		if !ok {
			continue
		}
		value := sheet.Rows[from].Cell(merge.FirstCol)
		if value == "" {
			continue
		}
		for number := merge.FirstRow + 1; number <= merge.LastRow; number++ {
			i, found := at[number]
			if !found {
				continue
			}
			fillCell(&sheet.Rows[i], merge.FirstCol, value)
		}
	}
}

// mergedColumnSpans is the merged blocks covering DATA cells that were not
// expanded because they run across columns rather than down one.
//
// A block that covers nothing but the header row is left out: one heading
// written across two columns costs the second column its heading, which is a
// different thing with a different remedy, and it is already reported as a
// column with no heading.
func mergedColumnSpans(sheet domain.Sheet, headerRow int) []string {
	var refs []string
	for _, merge := range sheet.Merges {
		if merge.FirstCol == merge.LastCol || merge.LastRow <= headerRow {
			continue
		}
		refs = append(refs, merge.Ref)
	}
	return refs
}

// fillCell writes a value into a cell the row does not reach, widening the row
// to hold it. A workbook's row ends at its last non-empty cell, so the cells a
// merge covers are usually not there at all.
func fillCell(row *domain.Row, col int, value string) {
	if col < len(row.Cells) {
		if row.Cells[col] == "" {
			row.Cells[col] = value
		}
		return
	}
	widened := make([]string, col+1)
	copy(widened, row.Cells)
	widened[col] = value
	row.Cells = widened
}

// firstValueAtOrAfter is the first column at or past the header's last one that
// actually holds something — the value the customer will recognise when they go
// and look, rather than whichever cell happens to sit one past the headings.
func firstValueAtOrAfter(row domain.Row, col int) int {
	for i := col; i < len(row.Cells); i++ {
		if row.Cells[i] != "" {
			return i
		}
	}
	return col
}

// significantWidth is how many columns a row actually uses: trailing empty
// cells are what a trailing delimiter and a formatted-but-empty column leave
// behind, and neither is a value.
func significantWidth(row domain.Row) int {
	for col := len(row.Cells) - 1; col >= 0; col-- {
		if row.Cells[col] != "" {
			return col + 1
		}
	}
	return 0
}
