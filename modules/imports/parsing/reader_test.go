package parsing

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// fixture is one of the testdata files — real export files, not tidied test
// data — as bytes.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

// read runs one of the fixtures through the whole reading half.
func read(t *testing.T, name string, target domain.Target) *domain.ReadOutcome {
	t.Helper()
	data := fixture(t, name)

	outcome, err := New().Read(context.Background(), domain.Upload{
		Target: target, FileName: name, Content: data,
	})
	require.NoError(t, err)
	require.NotNil(t, outcome)
	return outcome
}

func rowNumbers(outcome *domain.ReadOutcome) []int {
	out := make([]int, 0, len(outcome.Rows))
	for _, row := range outcome.Rows {
		out = append(out, row.Number)
	}
	return out
}

func fileMessages(outcome *domain.ReadOutcome) string {
	var sb strings.Builder
	for _, issue := range outcome.FileIssues {
		sb.WriteString(issue.Message)
		sb.WriteString("\n")
	}
	return sb.String()
}

func errorsIn(row domain.DraftRow) []string {
	var out []string
	for _, issue := range row.Issues {
		if issue.IsError() {
			out = append(out, issue.Message)
		}
	}
	return out
}

// An ERP export: a byte-order mark, a title block, CRLF line endings, a comma
// inside a quoted legal name, a blank line in the middle, a column ZenTax has
// no home for, and a summary line under the table. None of it is a reason to
// refuse the file.
func TestRead_AnERPExport(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_erp_export.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Equal(t, 4, outcome.HeaderRow, "the header is found under the title block")
	assert.Equal(t, "Entity Name", outcome.Columns[domain.FieldName])
	assert.Equal(t, []int{5, 6, 8, 9, 10}, rowNumbers(outcome),
		"a blank line in the middle shifts nothing: these are the customer's own row numbers")

	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}

	assert.Equal(t, []domain.IgnoredColumn{{Column: "H", Header: "Cost centre"}}, outcome.Ignored)
	assert.Contains(t, fileMessages(outcome), `H ("Cost centre")`)
	assert.Contains(t, fileMessages(outcome), "row 11 had nothing in any imported column")

	first := outcome.Rows[0].Entity
	require.NotNil(t, first)
	assert.Equal(t, "Acme Holding AG", first.Name)
	assert.Equal(t, "12-31", *first.FinancialYearEnd)
	assert.Empty(t, first.ParentRef, "the top of the group has no parent")

	second := outcome.Rows[1].Entity
	assert.Equal(t, "Acme Deutschland GmbH, Munich", *second.LegalName, "a comma inside a quoted cell is content")
	assert.Equal(t, "Acme Holding AG", second.ParentRef)

	byName := map[string]*domain.EntityDraft{}
	for _, row := range outcome.Rows {
		byName[row.Entity.Name] = row.Entity
	}
	assert.Equal(t, "445", byName["Acme France SAS"].FiscalCalendarPattern)
	assert.Equal(t, "04-05", *byName["Acme UK Limited"].FinancialYearEnd, "the UK tax year end, written MM-DD")
	assert.Equal(t, "12-31", *byName["Acme Retail Nordics AB"].FinancialYearEnd, "an ISO date, read as its month and day")
}

// A Windows Excel export from a comma-decimal locale: semicolon-separated, a
// title block above the table, German spellings of a date and a week end, and
// accented names written as the UTF-8 that "CSV UTF-8" saves. The separator is
// reported; the encoding is not, because there is nothing worth saying about a
// file that is what it ought to be. The same register saved as plain "CSV" — a
// single-byte codepage — is refused with its cells named instead, which is what
// encoding_test.go is about.
func TestRead_AWindowsExcelExportInAnotherLocale(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_excel_de.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Contains(t, fileMessages(outcome), "read as semicolon-separated values")
	assert.NotContains(t, fileMessages(outcome), "not UTF-8")
	assert.Equal(t, []int{5, 6, 7, 8}, rowNumbers(outcome))

	names := make([]string, 0, len(outcome.Rows))
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
		names = append(names, row.Entity.Name)
	}
	assert.Equal(t, []string{
		"Zentral Beteiligungs GmbH", "Zentral Österreich GmbH",
		"Zentral Retail España SL", "Zentral Logistik AG",
	}, names)

	assert.Equal(t, "12-31", *outcome.Rows[2].Entity.FinancialYearEnd, `"31.12." is unambiguous: 31 is not a month`)
	assert.Equal(t, "sunday", outcome.Rows[3].Entity.FiscalWeekEndDay)
	assert.Equal(t, "last", outcome.Rows[3].Entity.FiscalYearEndRule)
}

// A workbook written by something other than this reader's own library: shared
// strings, an inline string, a title block, a spacer column with no heading, a
// year end held as a real DATE cell under two different number formats, and a
// second sheet. The tab named for the target is the one read.
func TestRead_AWorkbookWrittenBySomethingElse(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_group_register.xlsx", domain.TargetEntities)

	assert.False(t, outcome.HasFileError())
	assert.Equal(t, 4, outcome.HeaderRow)
	assert.Equal(t, []int{5, 6, 8, 9}, rowNumbers(outcome))
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}

	// Rows 5 and 6 are date cells — one on the built-in "mm-dd-yy" format, one
	// on a custom "yyyy-mm-dd". Neither is read through the format it is shown
	// in, so neither can be read as the wrong day.
	assert.Equal(t, "12-31", *outcome.Rows[0].Entity.FinancialYearEnd)
	assert.Equal(t, "12-31", *outcome.Rows[1].Entity.FinancialYearEnd)
	// Row 8 holds the same value as padded text, row 9 as a month name.
	assert.Equal(t, "12-31", *outcome.Rows[2].Entity.FinancialYearEnd)
	assert.Equal(t, "04-05", *outcome.Rows[3].Entity.FinancialYearEnd)

	assert.Equal(t, "Acme Deutschland Gesellschaft mit beschränkter Haftung", *outcome.Rows[1].Entity.LegalName)
	assert.Equal(t, []domain.IgnoredColumn{{Column: "I", Header: "Responsible"}}, outcome.Ignored)
	assert.Contains(t, fileMessages(outcome), "row 11 had nothing in any imported column")
}

// The obligation register, with both deadline strategies side by side.
func TestRead_AnObligationRegister(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_register.csv", domain.TargetObligations)

	assert.False(t, outcome.HasFileError())
	require.Len(t, outcome.Rows, 5)
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}

	swissCIT := outcome.Rows[0].Obligation
	assert.Equal(t, "Acme Holding AG", swissCIT.EntityRef)
	assert.Equal(t, "CIT-ANN", swissCIT.ObligationTypeRef)
	assert.Equal(t, "annual", swissCIT.Periodicity)
	assert.Equal(t, "fixed", swissCIT.DeadlineRule.Type)
	assert.Equal(t, []string{"09-30"}, swissCIT.DeadlineRule.FixedDates)

	germanVAT := outcome.Rows[1].Obligation
	assert.Equal(t, "period_offset", germanVAT.DeadlineRule.Type)
	assert.Equal(t, 10, germanVAT.DeadlineRule.FilingOffset.Days)
	assert.Equal(t, 10, germanVAT.DeadlineRule.PaymentOffset.Days)
	assert.Equal(t, "next-business-day", germanVAT.DeadlineRule.WeekendAdjustment)

	frenchVAT := outcome.Rows[3].Obligation
	assert.Equal(t, 1, frenchVAT.DeadlineRule.FilingOffset.Months)
	assert.Nil(t, frenchVAT.DeadlineRule.PaymentOffset, "no payment columns means payment is due with the filing")

	assert.Equal(t, "EUR", *outcome.Rows[2].Obligation.Currency, "a lower-case code is upper-cased")
	assert.Equal(t, []domain.IgnoredColumn{{Column: "N", Header: "Owner"}}, outcome.Ignored)
}

// A file wrong in several ways at once: every problem is reported, and every
// one of them names the row and the cell.
func TestRead_AnEntityFileWrongInSeveralWays(t *testing.T) {
	t.Parallel()
	outcome := read(t, "entities_messy.csv", domain.TargetEntities)

	assert.False(t, outcome.HasFileError(), "the file itself is readable; its rows are not all valid")
	require.Len(t, outcome.Rows, 5)
	assert.Empty(t, errorsIn(outcome.Rows[0]), "the first row is fine, and stays fine")

	byRow := map[int][]string{}
	for _, row := range outcome.Rows {
		byRow[row.Number] = errorsIn(row)
	}

	require.Len(t, byRow[3], 1)
	assert.Contains(t, byRow[3][0], "row 2 already describes this entity")

	require.Len(t, byRow[4], 1)
	assert.Contains(t, byRow[4][0], `the "Country" cell is empty`)

	require.Len(t, byRow[5], 3, "one pass reports all three, not one per upload")
	assert.Contains(t, byRow[5][0], "is named as its own parent")
	assert.Contains(t, byRow[5][1], "could be 4 March (03-04) or 3 April (04-03)")
	assert.Contains(t, byRow[5][2], "custom fiscal calendar cannot be described in a spreadsheet row")

	require.Len(t, byRow[6], 1)
	assert.Contains(t, byRow[6][0], `"fiscal" is not a fiscal calendar ZenTax knows`)

	// Every error is addressed where the customer can act on it.
	for _, row := range outcome.Rows {
		for _, issue := range row.Issues {
			if issue.IsError() {
				assert.NotEmpty(t, issue.CellRef, issue.Message)
				assert.Equal(t, row.Number, issue.Row)
			}
		}
	}
}

func TestRead_AnObligationFileWrongInSeveralWays(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_messy.csv", domain.TargetObligations)

	assert.False(t, outcome.HasFileError())
	byRow := map[int][]string{}
	for _, row := range outcome.Rows {
		byRow[row.Number] = errorsIn(row)
	}

	require.Len(t, byRow[5], 1)
	assert.Contains(t, byRow[5][0], "three-letter ISO 4217 code")

	require.Len(t, byRow[6], 1)
	assert.Contains(t, byRow[6][0], `"each month" is not a frequency ZenTax knows`)

	require.Len(t, byRow[7], 1)
	assert.Contains(t, byRow[7][0], "row 6 already gives")

	require.Len(t, byRow[8], 1)
	assert.Contains(t, byRow[8][0], "two different ways to compute a deadline")

	require.Len(t, byRow[9], 1)
	assert.Contains(t, byRow[9][0], "cell is empty on this row")

	require.Len(t, byRow[10], 1, "the pairing check is not run on dates that could not be read")
	assert.Contains(t, byRow[10][0], "and the file does not say which")

	assert.Contains(t, fileMessages(outcome), "row 11 had nothing in any imported column")
	assert.Contains(t, fileMessages(outcome), `L ("Reviewer")`)
}

// A file that is not an entity register at all is refused as a file: there is
// no row-by-row report worth giving about somebody's accounts payable ledger.
func TestRead_RefusesAFileThatIsNotTheOneMeant(t *testing.T) {
	t.Parallel()
	outcome, err := New().Read(context.Background(), domain.Upload{
		Target:   domain.TargetEntities,
		FileName: "ledger.csv",
		Content:  []byte("Reference,Amount,Posted\nINV-1,100,2026-01-01\n"),
	})
	require.NoError(t, err)
	assert.True(t, outcome.HasFileError())
	assert.Empty(t, outcome.Rows)
	assert.Contains(t, fileMessages(outcome), "no header row found")
}

// The formats this product cannot read say so by name, with what to do.
func TestRead_RefusalsThatAreAboutTheFileItself(t *testing.T) {
	t.Parallel()
	reader := New()

	_, err := reader.ReadFile("book.xls", append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, 0, 0, 0, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy .xls workbook")
	assert.Contains(t, err.Error(), "Save As")

	_, err = reader.ReadFile("empty.csv", []byte("   \n\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")

	_, err = reader.ReadFile("broken.xlsx", []byte("PK\x03\x04not really a zip"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be opened")
}

// Past the row cap a file is refused outright — it is not half-read and
// half-reported.
func TestRead_RefusesAFileBeyondTheRowCap(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	sb.WriteString("Entity Name,Country\n")
	for i := 0; i < 20; i++ {
		sb.WriteString("Entity,Switzerland\n")
	}

	reader := NewWithLimits(Limits{
		MaxRows: 5, MaxColumns: 64, MaxSheets: 4, MaxCells: 4096,
		MaxUnzipBytes: 1 << 20, MaxPartBytes: 1 << 18, MaxZipParts: 64,
	})
	_, err := reader.ReadFile("big.csv", []byte(sb.String()))
	require.ErrorIs(t, err, domain.ErrTooManyRows)
}

// A tab-separated paste and a UTF-16 "Unicode text" export are both real, and
// both read.
func TestRead_TabsAndUTF16(t *testing.T) {
	t.Parallel()
	tabbed := "Entity Name\tCountry\tParent company\nAcme Holding AG\tSwitzerland\t\n"
	outcome, err := New().Read(context.Background(), domain.Upload{
		Target: domain.TargetEntities, FileName: "paste.txt", Content: []byte(tabbed),
	})
	require.NoError(t, err)
	require.Len(t, outcome.Rows, 1)
	assert.Equal(t, "Acme Holding AG", outcome.Rows[0].Entity.Name)
	assert.Contains(t, fileMessages(outcome), "read as tab-separated values")

	utf16le := []byte{0xFF, 0xFE}
	for _, r := range tabbed {
		utf16le = append(utf16le, byte(r), byte(r>>8))
	}
	outcome, err = New().Read(context.Background(), domain.Upload{
		Target: domain.TargetEntities, FileName: "unicode.txt", Content: utf16le,
	})
	require.NoError(t, err)
	require.Len(t, outcome.Rows, 1)
	assert.Equal(t, "Acme Holding AG", outcome.Rows[0].Entity.Name)
	assert.Contains(t, fileMessages(outcome), "UTF-16")
}

// A workbook whose tabs do not say which one to import is refused rather than
// guessed at: reading the wrong tab is a silent, expensive mistake.
func TestPickSheet_RefusesToGuessBetweenTwoDataSheets(t *testing.T) {
	t.Parallel()
	file := &domain.File{Format: domain.FormatXLSX, Sheets: []domain.Sheet{
		{Name: "Q1", Rows: []domain.Row{{Number: 1, Cells: []string{"Entity Name", "Country"}}}},
		{Name: "Q2", Rows: []domain.Row{{Number: 1, Cells: []string{"Entity Name", "Country"}}}},
	}}

	_, issues := file.PickSheet(domain.TargetEntities)
	require.Len(t, issues, 1)
	assert.True(t, issues[0].IsError())
	assert.Contains(t, issues[0].Message, "none of them is named for entities")
}

func TestNumberFormats_OnlyDateFormatsMakeADate(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"yyyy-mm-dd", `[$-409]d/m/yyyy`, "dd mmm yy", `mmm" "yyyy`} {
		assert.True(t, looksLikeDateFormat(code), code)
	}
	for _, code := range []string{"#,##0.00", "0.0%", "h:mm:ss", `#,##0" days"`, "mm:ss.0"} {
		assert.False(t, looksLikeDateFormat(code), code)
	}
}

// Every all-digit tax reference in every spreadsheet is stored as a NUMBER, and
// some of those numbers land inside the Excel date-serial range. A reference
// that came back as "2025-01-31", or as "1.2345678901e+10", would be wrong in a
// way nobody notices until a filing is rejected.
func TestRead_ANumberInACellIsNotADateJustBecauseItCouldBe(t *testing.T) {
	t.Parallel()
	outcome := read(t, "obligations_typed_as_numbers.xlsx", domain.TargetObligations)

	assert.False(t, outcome.HasFileError())
	require.Len(t, outcome.Rows, 2)
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
	}

	first := outcome.Rows[0].Obligation
	require.NotNil(t, first.TaxReferenceNumber)
	assert.Equal(t, "45657", *first.TaxReferenceNumber,
		"a plain-number cell stays a number even when its value is a valid date serial")
	assert.Equal(t, 10, first.DeadlineRule.FilingOffset.Days)

	second := outcome.Rows[1].Obligation
	require.NotNil(t, second.TaxReferenceNumber)
	assert.Equal(t, "12345678901", *second.TaxReferenceNumber,
		"a number a workbook stored in scientific notation is written back in full")
	assert.Equal(t, 30, second.DeadlineRule.FilingOffset.Days)
}
