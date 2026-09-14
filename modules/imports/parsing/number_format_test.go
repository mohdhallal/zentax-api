package parsing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// obligationBook writes a one-row entity-obligations workbook whose tax
// reference cell holds a NUMBER under the number format given.
//
// It is written with the reader's own library rather than by hand, unlike the
// testdata fixtures, because what is under test here is not how a workbook is
// packed but what its number format means: the same style any spreadsheet
// writes when a customer formats a column of identifiers so its leading zeros
// stay on screen.
func obligationBook(t *testing.T, numFmt string, reference int) []byte {
	t.Helper()
	book := excelize.NewFile()
	defer func() { require.NoError(t, book.Close()) }()

	const sheet = "Sheet1"
	require.NoError(t, book.SetSheetRow(sheet, "A1", &[]any{
		"Entity", "Obligation Type", "Tax Reference Number", "Periodicity", "Filing Offset Days",
	}))
	require.NoError(t, book.SetSheetRow(sheet, "A2", &[]any{
		"Meridian UK Trading", "VAT-RET", reference, "quarterly", 7,
	}))

	style, err := book.NewStyle(&excelize.Style{CustomNumFmt: &numFmt})
	require.NoError(t, err)
	require.NoError(t, book.SetCellStyle(sheet, "C2", "C2", style))

	buf, err := book.WriteToBuffer()
	require.NoError(t, err)
	return buf.Bytes()
}

func readBook(t *testing.T, data []byte) *domain.ReadOutcome {
	t.Helper()
	outcome, err := New().Read(context.Background(), domain.Upload{
		Target: domain.TargetObligations, FileName: "register.xlsx", Content: data,
	})
	require.NoError(t, err)
	require.NotNil(t, outcome)
	return outcome
}

// A tax reference kept in a padding format arrives with its leading zeros.
//
// The tax authority issued 0012345; the customer formatted the column
// "0000000" so the sheet shows 0012345; the workbook stores 12345 and keeps the
// zeros in the format. Reading the stored number drops two digits from a filing
// identifier — and says nothing, in a value nobody proofreads digit by digit.
func TestXLSX_ReadsATaxReferenceAsThePaddingFormatShowsIt(t *testing.T) {
	t.Parallel()
	outcome := readBook(t, obligationBook(t, "0000000", 12345))

	require.Len(t, outcome.Rows, 1)
	row := outcome.Rows[0]
	require.NotNil(t, row.Obligation)
	require.NotNil(t, row.Obligation.TaxReferenceNumber)
	assert.Equal(t, "0012345", *row.Obligation.TaxReferenceNumber,
		"the value the customer sees, not the number under it")
	assert.Empty(t, errorsIn(row))

	// The rest of the row is untouched: the offset column has no padding format,
	// so it is still read as the plain number it is.
	require.NotNil(t, row.Obligation.DeadlineRule.FilingOffset)
	assert.Equal(t, 7, row.Obligation.DeadlineRule.FilingOffset.Days)
}

// And an ordinary numeric format is still read as the number it holds. A
// thousands separator or two decimal places is how a QUANTITY is displayed;
// taking the displayed value there would import "12,345" as a reference and
// "10.00" as a count of days.
func TestXLSX_LeavesAnOrdinaryNumberFormatAlone(t *testing.T) {
	t.Parallel()
	for _, numFmt := range []string{"#,##0", "0.00", "0", "0%"} {
		outcome := readBook(t, obligationBook(t, numFmt, 12345))
		require.Len(t, outcome.Rows, 1, numFmt)
		require.NotNil(t, outcome.Rows[0].Obligation.TaxReferenceNumber, numFmt)
		assert.Equal(t, "12345", *outcome.Rows[0].Obligation.TaxReferenceNumber, numFmt)
	}
}

// The predicate itself, because it is the whole judgement: which formats pad
// with zeros a reader must not drop, and which are quantities.
func TestZeroPaddingFormat(t *testing.T) {
	t.Parallel()
	pads := []string{
		"0000000",     // The plain case: a seven-digit identifier.
		"00000",       // A postcode, and a Belgian enterprise number's first block.
		`00000\-0000`, // A padded identifier with a literal separator in it.
		"00-000-000",  // The same, written without the escape.
		"[Blue]000000",
		"000000;-000000",
	}
	for _, code := range pads {
		assert.True(t, zeroPaddingFormat(code), code)
	}

	quantities := []string{
		"0", "0.00", "#,##0", "#,##0.00", "0%", "0.00%", "0.00E+00", "# ?/?",
		"General", "@", `0" kg"`,
		"dd-mm-yyyy", "yyyy-mm-dd", "mmm yyyy", // Dates, which are the other question entirely.
	}
	for _, code := range quantities {
		assert.False(t, zeroPaddingFormat(code), code)
	}
}
