package parsing

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// These are the tests for the round trip: a file this product WROTE, read back
// by this product. The defect they exist against was a contradiction between
// two of our own fixes — the export marks a value a spreadsheet would evaluate
// with a leading apostrophe, and the import used to read that apostrophe as
// part of the name. Export, edit, re-upload, and a customer's register grew a
// second entity called `'+41 Kommunikations AG` beside the one they meant to
// update, with no file issue and no row issue anywhere in the report.
//
// The contract both halves now obey is written down in textmarker.go here and
// at the top of zentax-ui/client/src/lib/export-cells.ts there.

// asTheExportWritesIt writes the bytes the product's own CSV export writes: the
// UTF-8 byte-order mark, every field quoted with embedded quotation marks
// doubled, and the text marker in front of every value the export marks.
//
// It is csvCell and guardFormula (client/src/lib/export-cells.ts) transcribed,
// because a round-trip test is only worth the file it reads: the bytes have to
// be what the OTHER half writes rather than what this half finds convenient.
// The transcription is held to the literal bytes by the assertions in
// TestRead_TheProductsOwnExportReadsBackAsItself, so a drift between the two
// halves cannot cancel itself out here.
func asTheExportWritesIt(rows [][]string) []byte {
	var sb strings.Builder
	sb.Write(utf8BOM) // the mark every CSV the product writes carries (sniff.go)

	for i, row := range rows {
		if i > 0 {
			sb.WriteString("\n")
		}
		for j, cell := range row {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`"` + strings.ReplaceAll(guardFormula(cell), `"`, `""`) + `"`)
		}
	}
	return []byte(sb.String())
}

// guardFormula is the export's own rule: the marker goes on a value a
// spreadsheet would evaluate, and on one that already carries a marker.
func guardFormula(value string) string {
	if needsTextMarker(value) || carriesTextMarker(value) {
		return textMarker + value
	}
	return value
}

// readContent reads bytes assembled in this file (rather than a testdata
// fixture) through the whole reading half.
func readContent(t *testing.T, name string, data []byte, target domain.Target) *domain.ReadOutcome {
	t.Helper()
	outcome, err := New().Read(context.Background(), domain.Upload{
		Target: target, FileName: name, Content: data,
	})
	require.NoError(t, err)
	require.NotNil(t, outcome)
	return outcome
}

// The whole contract in one test: every value that leaves through the export
// comes back through the import as itself, and is therefore matched against the
// record it came from rather than creating a second one.
func TestRead_TheProductsOwnExportReadsBackAsItself(t *testing.T) {
	t.Parallel()
	names := []string{
		"+41 Kommunikations AG",  // a phone-code name: marked, because `+` leads a formula
		"@Home Services Ltd",     // marked
		"=Audit Partners GmbH",   // marked
		"-Nordic Holding AS",     // marked
		"Müller & Söhne GmbH",    // not marked, and already round-tripped before this fix
		"'+41 Kommunikations SA", // genuinely begins with an apostrophe: marked TWICE
		"'t Veld Beheer B.V.",    // a Dutch legal name: not marked, not stripped
	}
	rows := [][]string{{"Entity Name", "Country"}}
	for _, name := range names {
		rows = append(rows, []string{name, "Germany"})
	}
	data := asTheExportWritesIt(rows)

	// What the export actually put in the file, byte for byte — so the mirror
	// above is pinned to a literal and not merely to this package's own rule.
	text := string(data)
	assert.Contains(t, text, `"'+41 Kommunikations AG"`, "the marker a spreadsheet needs")
	assert.Contains(t, text, `"''+41 Kommunikations SA"`, "and a second one where the value carries one itself")
	assert.Contains(t, text, `"'t Veld Beheer B.V."`, "an apostrophe that is not a marker is left alone")

	outcome := readContent(t, "entities-export-2026-09-14.csv", data, domain.TargetEntities)
	require.False(t, outcome.HasFileError(), fileMessages(outcome))
	require.Len(t, outcome.Rows, len(names))

	for i, row := range outcome.Rows {
		require.NotNil(t, row.Entity, "row %d", row.Number)
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
		assert.Equal(t, names[i], row.Entity.Name, "the name that went out is the name that comes back")
		assert.Equal(t, domain.NormalizeKey(names[i]), row.NaturalKey(),
			"and it matches the record it came from rather than creating a second one")
	}
}

// Taking the marker off is a reading decision, and this package says every
// reading decision out loud: which cell, and what it was read as. A customer
// whose apostrophe was meant literally can see that it went and say so.
func TestRead_TheMarkerComingOffIsSaidOutLoud(t *testing.T) {
	t.Parallel()
	data := asTheExportWritesIt([][]string{
		{"Entity Name", "Country"},
		{"+41 Kommunikations AG", "Switzerland"},
	})

	outcome := readContent(t, "entities.csv", data, domain.TargetEntities)
	messages := fileMessages(outcome)
	assert.False(t, outcome.HasFileError(), messages)
	assert.Contains(t, messages, "A2 begins with an apostrophe in front of a formula character")
	assert.Contains(t, messages, `A2 was read as "+41 Kommunikations AG"`)
	assert.Contains(t, messages, "write it twice", "the remedy, which this reader honours")
}

// Several of them are counted rather than listed, because a register exported
// from this product carries the marker on every row that needs it.
func TestRead_ManyMarkedCellsAreCountedRatherThanListed(t *testing.T) {
	t.Parallel()
	rows := [][]string{{"Entity Name", "Country"}}
	for _, name := range []string{"+1 Alpha AG", "+2 Beta AG", "+3 Gamma AG", "+4 Delta AG", "+5 Epsilon AG"} {
		rows = append(rows, []string{name, "Germany"})
	}

	outcome := readContent(t, "entities.csv", asTheExportWritesIt(rows), domain.TargetEntities)
	messages := fileMessages(outcome)
	assert.Contains(t, messages, "5 cells begin with an apostrophe")
	assert.Contains(t, messages, `A2 was read as "+1 Alpha AG"`)
	assert.Contains(t, messages, "2 more")
	assert.NotContains(t, messages, "+5 Epsilon AG", "the fifth is counted, not quoted")
}

// An apostrophe in front of anything that is not a formula character is part of
// the value, and neither half of the contract touches it. Nothing is said about
// these cells either, because nothing was done to them.
func TestRead_AnApostropheThatIsNotAMarkerIsPartOfTheValue(t *testing.T) {
	t.Parallel()
	outcome := readContent(t, "entities.csv", []byte(
		"Entity Name,Country\n"+
			"'t Veld Beheer B.V.,Netherlands\n"+
			"'s-Hertogenbosch Vastgoed N.V.,Netherlands\n"), domain.TargetEntities)

	require.Len(t, outcome.Rows, 2)
	assert.Equal(t, "'t Veld Beheer B.V.", outcome.Rows[0].Entity.Name)
	assert.Equal(t, "'s-Hertogenbosch Vastgoed N.V.", outcome.Rows[1].Entity.Name)
	assert.NotContains(t, fileMessages(outcome), "apostrophe")
}

// A workbook carries the marker as a cell FORMAT (`quotePrefix`), never as
// characters, so an apostrophe in an .xlsx cell is somebody's apostrophe and
// this rule must not go near it.
func TestRead_AWorkbookApostropheIsNeverAMarker(t *testing.T) {
	t.Parallel()
	book := excelize.NewFile()
	defer func() { _ = book.Close() }()
	require.NoError(t, book.SetSheetName("Sheet1", "Entities"))
	require.NoError(t, book.SetCellStr("Entities", "A1", "Entity Name"))
	require.NoError(t, book.SetCellStr("Entities", "B1", "Country"))
	require.NoError(t, book.SetCellStr("Entities", "A2", "'+41 Kommunikations AG"))
	require.NoError(t, book.SetCellStr("Entities", "B2", "Switzerland"))
	buf, err := book.WriteToBuffer()
	require.NoError(t, err)

	outcome := readContent(t, "entities.xlsx", buf.Bytes(), domain.TargetEntities)
	require.Len(t, outcome.Rows, 1)
	assert.Equal(t, "'+41 Kommunikations AG", outcome.Rows[0].Entity.Name,
		"a workbook's apostrophe is a character in the value, and stays one")
	assert.NotContains(t, fileMessages(outcome), "apostrophe")
}

// The rule itself, value by value: it strips exactly what the export marks, and
// nothing else. Each line is a value a real register carries.
func TestTextMarker_StripsExactlyWhatTheExportMarks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string // the cell as it stands in the file
		want string // the value read out of it
		why  string
	}{
		{"'+41 Kommunikations AG", "+41 Kommunikations AG", "the marked form of a name a spreadsheet would evaluate"},
		{"'=cmd|'/C calc.exe'!A0", "=cmd|'/C calc.exe'!A0", "including the payload the marker exists for"},
		{"''+41 Kommunikations SA", "'+41 Kommunikations SA", "one apostrophe comes off, not both"},
		{"'''-Nordic", "''-Nordic", "and only ever one, however many there are"},
		{"'t Veld Beheer B.V.", "'t Veld Beheer B.V.", "a Dutch name: the apostrophe leads a letter, not a formula"},
		{"'0012345", "'0012345", "a tax reference marked as text: a digit is not a formula lead, so it stays as written"},
		{"'-500", "'-500", "the export never marks a number, so this apostrophe is not the export's"},
		{"'-", "'-", "a lone trigger character is not marked either"},
		{"-500", "-500", "an unmarked number is untouched"},
		{"-1,234.50", "-1,234.50", "grouped the way an export writes it, and still a number"},
		{"+3e4", "+3e4", "an exponent is a number too"},
		{"=SUM(A1:A2)", "=SUM(A1:A2)", "an unmarked formula is content, and refused elsewhere if it is not a value"},
		{"Müller & Söhne GmbH", "Müller & Söhne GmbH", "an ordinary name"},
		{"", "", "an empty cell"},
	}
	for _, c := range cases {
		rows := []domain.Row{{Number: 2, Cells: []string{c.file}}}
		stripTextMarkers(rows)
		assert.Equal(t, c.want, rows[0].Cells[0], c.why)
	}
}

// The two halves are inverses, which is a stronger claim than either rule on
// its own: write any value out and read it back, and it is the value.
func TestTextMarker_TheExportAndTheImportAreInverses(t *testing.T) {
	t.Parallel()
	values := []string{
		"+41 Kommunikations AG", "@Home Services Ltd", "=Audit Partners GmbH", "-Nordic Holding AS",
		"'+41 Kommunikations AG", "''=Audit Partners GmbH", "'t Veld Beheer B.V.", "'0012345", "'-500",
		"-500", "-1,234.50", "+3e4", "-", "=", "Müller & Söhne GmbH", "", "  ", "\t=1+1", "\r=1+1",
	}
	for _, value := range values {
		rows := []domain.Row{{Number: 2, Cells: []string{guardFormula(value)}}}
		stripTextMarkers(rows)
		assert.Equal(t, value, rows[0].Cells[0], "exported and read back: %q", value)
	}
}
