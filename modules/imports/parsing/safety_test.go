package parsing

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// These are the tests for reading a file WHOLE, and for reading each value as
// what it says. Everything they cover used to pass silently: a smaller row
// count than the customer's file holds, a deadline dropped on the floor, a
// broken lookup stored as a country, a workbook that filled the task's memory.
// Silence is what each of them asserts against.

// readFile is the reading half up to the sheets, for the files that are refused
// before any row exists.
func readFile(t *testing.T, name string) (*domain.File, error) {
	t.Helper()
	data := fixture(t, name)
	return New().ReadFile(name, data)
}

// A quotation mark in the one column this import does not even read used to
// delete every row after it — with no error, no warning, and a committable
// batch showing one entity where the file holds four.
func TestRead_AQuotationMarkThatNeverClosesIsRefusedByLine(t *testing.T) {
	t.Parallel()
	_, err := readFile(t, "entities_owner_quoted.csv")

	require.Error(t, err, "four entities must never be reported as one")
	assert.Contains(t, err.Error(), "row 5")
	assert.Contains(t, err.Error(), "cell F5", "the column the quotation mark is in")
	assert.Contains(t, err.Error(), "lines 6–8", "and the lines it ate")
	assert.Contains(t, err.Error(), "3 rows")
}

// A file can hold more than one runaway quote, and then the row count it parses
// to looks plausible rather than absurd — which is harder to notice, not
// easier. The rest are counted, so the refusal says how much is still wrong.
func TestRead_MoreThanOneRunawayQuoteIsCounted(t *testing.T) {
	t.Parallel()
	// The second quotation mark on line 3 is what closes the value line 2
	// opened; line 4 then opens another that runs to the end.
	_, err := New().ReadFile("register.csv", []byte(
		"Entity Name,Country,Owner\n"+
			"Acme One Ltd,United Kingdom,\"A. Meier\" (group)\n"+
			"Acme Two Ltd,United Kingdom,\"\n"+
			"Acme Three Ltd,United Kingdom,\"L. Dubois\" (VAT)\n"+
			"Acme Four Ltd,United Kingdom,K. Schmidt\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "row 2")
	assert.Contains(t, err.Error(), "1 further row below it does the same thing")
}

// The same character used the way it is used far more often — an inch mark, a
// nickname — is content, and stays content. It is said out loud, because a
// reader that decides this silently is a reader nobody can debug.
func TestRead_AQuotationMarkInsideAValueIsContent(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_inch_marks.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	require.Len(t, outcome.Rows, 3, "nothing is swallowed, so nothing is lost")
	assert.Equal(t, `Nordwind 5" Rohrsysteme GmbH`, *outcome.Rows[0].Entity.LegalName)
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}
	assert.Contains(t, fileMessages(outcome), "quotation mark on line 2, character 30")
	assert.Contains(t, fileMessages(outcome), "read as part of the value")
}

// A value that legitimately runs over several lines — a registered office — is
// quoted properly, so it is one cell and the rows either side of it are rows.
// The check for a runaway quote must not turn this into a refusal.
func TestRead_AValueThatRunsOverSeveralLines(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_registered_offices.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Equal(t, []int{2, 4, 6}, rowNumbers(outcome),
		"a two-line cell costs the file a line number, and none of its rows")
	assert.NotContains(t, fileMessages(outcome), "quotation mark")
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}
}

// The semicolon is both this import's documented separator for several filing
// dates AND the column separator of every Excel CSV written where the decimal
// separator is a comma. Unquoted, the January return used to vanish under a
// warning about the number of dates — and the row still committed.
func TestRead_AMultiValueCellSplitByTheFilesOwnDelimiter(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_excel_de_split_dates.csv", domain.TargetObligations)

	assert.True(t, outcome.HasFileError(), "a lost statutory deadline is not a warning")
	messages := fileMessages(outcome)
	assert.Contains(t, messages, "row 6 carries 10 values")
	assert.Contains(t, messages, "9 column headings on row 4")
	assert.Contains(t, messages, `"01-31" in J6`, "the deadline that would have been dropped")
	assert.Contains(t, messages, "wrapped in quotation marks")
}

// The same register written properly: the multi-value cell quoted, and the two
// spellings a German workbook writes a number in.
func TestRead_TheSameGermanRegisterWrittenProperly(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_excel_de.csv", domain.TargetObligations)

	assert.False(t, outcome.HasFileError())
	require.Len(t, outcome.Rows, 5)

	byRow := map[int]*domain.ObligationDraft{}
	errsByRow := map[int][]string{}
	for _, row := range outcome.Rows {
		byRow[row.Number], errsByRow[row.Number] = row.Obligation, errorsIn(row)
	}

	assert.Equal(t, []string{"07-31", "01-31"}, byRow[5].DeadlineRule.FixedDates,
		"a quoted cell holds both filing dates, and both are read")
	assert.Empty(t, errsByRow[5])

	assert.Equal(t, 10, byRow[6].DeadlineRule.FilingOffset.Days)

	require.Len(t, errsByRow[7], 1, `"1.000" is one thousand in this file's own convention`)
	assert.Contains(t, errsByRow[7][0], "must be a whole number of days")
	assert.Contains(t, errsByRow[7][0], `"1.000"`)

	require.Len(t, errsByRow[8], 1, "the comma spelling was already refused, and still is")
	assert.Contains(t, errsByRow[8][0], `"2,0"`)

	assert.Empty(t, errsByRow[9], `"2.0" is what a formatted cell hands over, and still reads`)
	assert.Equal(t, 2, byRow[9].DeadlineRule.FilingOffset.Days)
}

// The same split, in the shape that leaves no trace in the table at all: the
// columns after the split cell are blank, so the row occupies no more columns
// than the header does and the width check above sees nothing wrong with it.
// Every value in the row is then individually valid and every one of them is
// under the wrong heading — the filing date read as a payment date, and a
// December return that is never scheduled.
func TestRead_ASplitCellThatLeavesTheRowNoWiderThanTheHeader(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_excel_de_shifted_dates.csv", domain.TargetObligations)

	assert.True(t, outcome.HasFileError(), "a row read under the wrong headings is never a warning")
	messages := fileMessages(outcome)
	assert.Contains(t, messages, "row 5 was read as 10 values")
	assert.Contains(t, messages, "9 column headings on row 4")
	assert.Contains(t, messages, `"01-31" in H5 was read as "Payment dates"`,
		"the value that moved, and the heading it landed under")
	assert.Contains(t, messages, "wrapped in quotation marks")

	// The row still reads — the report shows what WOULD have been imported —
	// and what it reads is exactly the thing the refusal is about.
	require.Len(t, outcome.Rows, 2)
	shifted := outcome.Rows[0].Obligation
	require.NotNil(t, shifted)
	assert.Equal(t, []string{"07-31"}, shifted.DeadlineRule.FixedDates)
	assert.Equal(t, []string{"01-31"}, shifted.DeadlineRule.PaymentFixedDates,
		"the January FILING date, read as a payment date: the January return is never scheduled")
}

// The same defect on an entity register, where it used to produce no remark of
// any kind: a semicolon inside a legal name moves the country into the tax
// residency and a fragment of the name into the country, and the deadline
// engine reads the country.
func TestRead_ASplitCellOnAnEntityRowSaysSoToo(t *testing.T) {
	t.Parallel()
	outcome, err := New().Read(context.Background(), domain.Upload{
		Target:   domain.TargetEntities,
		FileName: "register.csv",
		Content: []byte("Entity Name;Legal name;Country of incorporation;Tax residency;Parent company\n" +
			"Acme Holding GmbH;Acme GmbH; vormals Beta;Germany;;\n"),
	})
	require.NoError(t, err)

	assert.True(t, outcome.HasFileError())
	assert.Contains(t, fileMessages(outcome), "row 2 was read as 6 values")
	assert.Contains(t, fileMessages(outcome), `"Germany" in D2 was read as "Tax residency"`)
}

// A register assembled with lookups against another sheet, half of which did
// not resolve. "#N/A" is never a value — and as a NAME it is the entity's
// natural key, so importing it strands a record that every repaired re-import
// walks straight past.
func TestRead_ASpreadsheetsOwnErrorTextIsNotAValue(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_broken_lookups.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError(), "the file is readable; four of its rows are not valid")
	byRow := map[int][]string{}
	for _, row := range outcome.Rows {
		byRow[row.Number] = errorsIn(row)
	}

	assert.Empty(t, byRow[2], "the row whose lookups resolved is fine, and stays fine")
	for _, row := range []int{3, 4, 5, 6} {
		require.Len(t, byRow[row], 1, "row %d", row)
		assert.Contains(t, byRow[row][0], "is a spreadsheet's own error text, not a value")
	}
	assert.Contains(t, byRow[3][0], `"#N/A" in "Entity Name"`)
	assert.Contains(t, byRow[4][0], `"#REF!" in "Legal name"`)
	assert.Contains(t, byRow[5][0], `"#VALUE!" in "Country"`)

	// The same values reach the workbook path as a formula's cached result.
	workbook := read(t, "entities_broken_lookups.xlsx", domain.TargetEntities)
	assert.False(t, workbook.HasFileError())
	require.Len(t, workbook.Rows, 2)
	assert.Empty(t, errorsIn(workbook.Rows[0]))
	require.Len(t, errorsIn(workbook.Rows[1]), 1)
	assert.Contains(t, errorsIn(workbook.Rows[1])[0], `"#N/A" in "Legal name"`)
}

// A formula cell whose workbook saved no result reads as an empty cell, and an
// empty cell in a column the file includes is a statement that clears the
// stored value. Reading it as empty would not be a missing value; it would be
// an erasure, so the workbook is refused and says which cell.
func TestRead_AFormulaSavedWithNoResult(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_unsaved_formulas.xlsx", domain.TargetEntities)

	assert.True(t, outcome.HasFileError())
	messages := fileMessages(outcome)
	assert.Contains(t, messages, `the sheet "Entities" holds 1 cell whose formula was saved with no result (B3)`)
	assert.Contains(t, messages, "save it so the formulas are calculated")

	// The rows are still read, and the formulas that DID carry a result are
	// read from it — the refusal is about the one cell that says nothing.
	require.Len(t, outcome.Rows, 3)
	assert.Equal(t, "Müller Holding GmbH Gesellschaft mbH", *outcome.Rows[0].Entity.LegalName)
	assert.Nil(t, outcome.Rows[1].Entity.LegalName)
	assert.Equal(t, "Müller Süd GmbH Gesellschaft mbH", *outcome.Rows[2].Entity.LegalName)
}

// A register merges the country cell down the rows it covers rather than typing
// it three times — which is what a merged cell MEANS. Read cell by cell, only
// the first row of the block holds anything: the other rows used to lose the
// value with nothing said, and on a second import the blank would have cleared
// what was stored, because a blank in a column the file includes is a statement.
func TestRead_AMergedCellCoversEveryRowItSpans(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_merged_register.xlsx", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	require.Len(t, outcome.Rows, 4)
	for _, row := range outcome.Rows[:3] {
		require.NotNil(t, row.Entity, "row %d", row.Number)
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
		assert.Equal(t, "Ireland", row.Entity.Country, "row %d reads what the merged cell covers", row.Number)
		require.NotNil(t, row.Entity.TaxResidency, "row %d", row.Number)
		assert.Equal(t, "Ireland", *row.Entity.TaxResidency, "row %d", row.Number)
	}

	// The same habit applied ACROSS columns cannot be read the same way: one
	// value under two headings that mean different things would be inventing
	// the second. Row 5 writes "France" across the country and the residency
	// columns, so the residency cell is read as empty — and an empty cell in a
	// column the file includes clears what is stored. Not filled in, then, but
	// never in silence either.
	france := outcome.Rows[3].Entity
	require.NotNil(t, france)
	assert.Equal(t, "France", france.Country)
	assert.Nil(t, france.TaxResidency)
	assert.Contains(t, fileMessages(outcome), "B5:C5")
	assert.Contains(t, fileMessages(outcome), "read as empty")
}

// A heading written across two columns leaves the second column with no heading
// of its own. It cannot be imported — there is nothing to bind it by — but it
// used to be dropped before the bookkeeping ran, so it was not even listed
// among the columns that would not arrive, which is the one disclosure this
// import promises about them.
func TestRead_AColumnWithNoHeadingIsNamedAmongTheOnesNotImported(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_merged_heading.xlsx", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Equal(t, []domain.IgnoredColumn{{Column: "D", Header: domain.NoHeading}}, outcome.Ignored)
	assert.Contains(t, fileMessages(outcome), "D (no heading)")

	// What that column holds is the day the fiscal week ends on, which decides
	// every period boundary a 4-4-5 entity has. The file says sunday and the
	// import applies its own default instead — so the one thing that makes the
	// difference visible is the column being named.
	require.Len(t, outcome.Rows, 2)
	assert.Equal(t, "445", outcome.Rows[0].Entity.FiscalCalendarPattern)
	assert.Equal(t, "saturday", outcome.Rows[0].Entity.FiscalWeekEndDay)
}

// A blank-headed column with nothing under it is a spacer, and a spacer is not
// a loss: it stays silent.
func TestRead_ASpacerColumnIsNotReported(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_group_register.xlsx", domain.TargetEntities)

	assert.Equal(t, []domain.IgnoredColumn{{Column: "I", Header: "Responsible"}}, outcome.Ignored,
		"column G has an empty heading and nothing under it")
	assert.NotContains(t, fileMessages(outcome), "no heading")
}

// A hidden tab matches a target's name exactly as well as a visible one, so
// last quarter's hidden copy used to win over the register being maintained —
// and every row of the answer looked plausible, because stale data looks like
// data. The visible sheet is read, the hidden one is named, and WHICH sheet was
// read is reported whether or not the choice was difficult.
func TestRead_AHiddenTabNeverWinsOverAVisibleOne(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_hidden_sheet.xlsx", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	names := make([]string, 0, len(outcome.Rows))
	for _, row := range outcome.Rows {
		names = append(names, row.Entity.Name)
	}
	assert.Equal(t, []string{"Acme Holding Ltd", "Acme Trading Ltd"}, names,
		"the register being maintained, not the hidden copy of last quarter's")

	messages := fileMessages(outcome)
	assert.Contains(t, messages, `read from the sheet "Working copy"`)
	assert.Contains(t, messages, `a hidden sheet named "Entities"`)
	assert.Contains(t, messages, "unhide it")
}

// Which tab was read is reported for every workbook, including the ones where
// the choice was obvious. It is one line, and it is the only line in the report
// that can give away a wrong-tab import.
func TestRead_TheSheetThatWasReadIsAlwaysNamed(t *testing.T) {
	t.Parallel()
	assert.Contains(t, fileMessages(read(t, "entities_group_register.xlsx", domain.TargetEntities)),
		`read from the sheet "Entities" — it is the tab named for entities.`)
	assert.Contains(t, fileMessages(read(t, "obligations_typed_as_numbers.xlsx", domain.TargetObligations)),
		`read from the sheet "Obligations"`)

	// A delimited file's one sheet is its own file name, so there is nothing
	// there for anybody to check and nothing is said about it.
	assert.NotContains(t, fileMessages(read(t, "entities_erp_export.csv", domain.TargetEntities)),
		"read from the sheet")
}

// Two tables stacked on one sheet: the rows above the header row used to be
// dropped in silence, while the same rows below it were named. Five rows in,
// two rows out, "This will create 2 entities", and nothing at all about the
// three that were stepped over.
func TestRead_RowsAboveTheHeaderAreNamed(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_two_tables.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Equal(t, 5, outcome.HeaderRow)
	assert.Equal(t, []int{6, 7}, rowNumbers(outcome))
	assert.Contains(t, fileMessages(outcome), "the column headings are on row 5, so rows 1, 2, 3 above them were not imported")
}

// A "Unicode text" export that lost its byte-order mark — trimmed, concatenated,
// or written by a tool that never adds one — used to be read as if it were
// UTF-8, because a NUL is valid UTF-8. Every value then carried a stray byte
// between its characters, the header still bound, the report looked readable,
// and the upload died much later on a database error about none of this.
func TestRead_UTF16WithNoByteOrderMark(t *testing.T) {
	t.Parallel()
	text := "Entity Name\tCountry\nAcme Holding AG\tSwitzerland\nAcme Süd GmbH\tGermany\n"

	for _, endian := range []struct {
		name  string
		bytes func(rune) []byte
	}{
		{"little-endian", func(r rune) []byte { return []byte{byte(r), byte(r >> 8)} }},
		{"big-endian", func(r rune) []byte { return []byte{byte(r >> 8), byte(r)} }},
	} {
		var content []byte
		for _, r := range text {
			content = append(content, endian.bytes(r)...)
		}

		outcome, err := New().Read(context.Background(), domain.Upload{
			Target: domain.TargetEntities, FileName: "unicode.txt", Content: content,
		})
		require.NoError(t, err, endian.name)
		require.Len(t, outcome.Rows, 2, endian.name)
		assert.Equal(t, "Acme Holding AG", outcome.Rows[0].Entity.Name, endian.name)
		assert.Equal(t, "Acme Süd GmbH", outcome.Rows[1].Entity.Name, endian.name)
		assert.Contains(t, fileMessages(outcome), "UTF-16 ("+endian.name+", with no byte-order mark)")
	}
}

// And if that recognition is ever missed, nothing that is not text reaches a
// value: a NUL is not storable in the database at all, so a file carrying one
// is refused by cell here rather than dying later on an error about neither the
// file nor the cell.
func TestRead_ACharacterThatIsNotTextIsRefusedByCell(t *testing.T) {
	t.Parallel()
	_, err := New().Read(context.Background(), domain.Upload{
		Target:   domain.TargetEntities,
		FileName: "register.csv",
		Content:  []byte("Entity Name,Country\nAcme\x00Holding AG,Switzerland\n"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A2")
	assert.Contains(t, err.Error(), `"Acme\u0000Holding AG"`, "the invisible made visible")
	assert.Contains(t, err.Error(), "without a byte-order mark")
}

// A workbook is a zip, and deflate compresses a sheet of one repeated character
// about a thousand times. The upload byte cap and the row cap are both spent
// too late to help: the memory goes while the archive is being expanded, long
// before there is a row to count. So the archive's own table of contents is
// read first, and a file that says it unpacks past the bound never gets opened.
func TestRead_RefusesAWorkbookByWhatItUnpacksTo(t *testing.T) {
	t.Parallel()
	bomb := workbookUnpackingTo(t, 64<<20)

	assert.Less(t, len(bomb), 200<<10, "the upload is tiny; what it unpacks to is not")
	_, err := New().ReadFile("entities.xlsx", bomb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpacks to more than 16 MB")
	assert.Contains(t, err.Error(), "export the one sheet you want to import as CSV")
}

// A sheet inside both the row cap and the column cap can still be a million
// cells, which is where the cost of a workbook actually lands.
func TestRead_RefusesASheetWithMoreCellsThanAnImportMayHold(t *testing.T) {
	t.Parallel()
	reader := NewWithLimits(Limits{
		MaxRows: 1000, MaxColumns: 64, MaxSheets: 4, MaxCells: 12,
		MaxUnzipBytes: 1 << 20, MaxPartBytes: 1 << 18, MaxZipParts: 64,
	})

	_, err := reader.ReadFile("register.xlsx", fixture(t, "entities_group_register.xlsx"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cells in its used range")
	assert.Contains(t, err.Error(), "at most 12")
}

// A file that is not a spreadsheet is named by what it IS. Told "no header row
// found ... it needs at least \"name\" and \"country\" as column headings", a
// customer goes looking for column headings in a file that has no columns.
func TestRead_RefusesFilesThatAreNotSpreadsheetsByName(t *testing.T) {
	t.Parallel()
	reader := New()

	// The commonest of them all: the HTML table an ERP's "Export to Excel"
	// button writes and names .xls.
	_, err := readFile(t, "entities_erp_export.xls")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a web page saved with a spreadsheet's file extension")
	assert.NotContains(t, err.Error(), "header row")

	_, err = reader.ReadFile("register.csv", []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this is a PDF")

	_, err = reader.ReadFile("register.xlsx", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 13})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a PNG image")

	_, err = reader.ReadFile("register.xml", []byte(
		`<?xml version="1.0"?><Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet"><Worksheet/></Workbook>`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an XML Spreadsheet 2003 file",
		"re-saving as .xlsx works for this one, and for a web page it does not")
}

// The compound-file magic belongs to a legacy .xls, a .doc AND a
// password-protected workbook alike. Calling every one of them a legacy .xls
// sent anyone with a protected register round a loop: "Save As .xlsx" keeps the
// password, and produces a file with this same magic.
func TestRead_TheOLEContainerRefusalNamesBothFilesItCouldBe(t *testing.T) {
	t.Parallel()
	_, err := New().ReadFile("book.xls",
		append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, 0, 0, 0, 0))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "either a legacy .xls workbook or a password-protected workbook")
	assert.Contains(t, err.Error(), "remove the password")
	assert.Contains(t, err.Error(), "saving it again as .xlsx keeps the password")
}

// workbookUnpackingTo builds an archive whose one part unpacks to the given
// size. It is written rather than committed as a fixture because a fixture like
// this is not a file anybody exports — it is the shape of an attack, and the
// point of the test is how small it is on the wire.
func workbookUnpackingTo(t *testing.T, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	part, err := archive.Create("xl/worksheets/sheet1.xml")
	require.NoError(t, err)

	chunk := []byte(strings.Repeat("A", 1<<20))
	for written := 0; written < size; written += len(chunk) {
		_, err := part.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	return buf.Bytes()
}
