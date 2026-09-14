package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sheetOf builds a sheet whose row numbers are the real ones, the way the
// reading half hands them over.
func sheetOf(rows ...[]string) Sheet {
	out := Sheet{Name: "Sheet1", Format: FormatCSV}
	for i, cells := range rows {
		out.Rows = append(out.Rows, Row{Number: i + 1, Cells: cells})
	}
	return out
}

// A header spelled the way a customer spells it binds the same field: case,
// spacing, punctuation, a required-field asterisk and a parenthesised unit hint
// all cost nothing.
func TestNormalizeHeader_FoldsWhatSpellingVaries(t *testing.T) {
	t.Parallel()
	for _, header := range []string{
		"Entity Name", "entity name", "ENTITY_NAME", "Entity  Name *",
		"Entity Name (required)", "entity-name", " Entity Name ",
	} {
		field, ok := FieldForHeader(TargetEntities, header)
		require.True(t, ok, header)
		assert.Equal(t, FieldName, field, header)
	}
}

// A header nobody listed binds nothing. Matching is exact on the folded form,
// never approximate: a column bound to the wrong field is the one import
// failure a customer cannot see in the report.
func TestFieldForHeader_DoesNotGuess(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"Entty Name", "Name of entity", "Cost centre", "Owner", ""} {
		_, ok := FieldForHeader(TargetEntities, header)
		assert.False(t, ok, header)
	}
}

// Two fields claiming the same spelling would make binding depend on map order.
// The catalogue is built at startup and panics on one; this is the test that
// says so out loud.
func TestFieldCatalogue_HasNoRepeatedAlias(t *testing.T) {
	t.Parallel()
	for _, target := range []Target{TargetEntities, TargetObligations} {
		seen := map[string]string{}
		for _, f := range Fields(target) {
			require.NotEmpty(t, f.Aliases, f.Name)
			require.NotEmpty(t, f.Help, f.Name)
			for _, alias := range f.Aliases {
				normalized := NormalizeHeader(alias)
				require.NotEmpty(t, normalized, alias)
				if other, clash := seen[normalized]; clash {
					t.Fatalf("%s: alias %q is claimed by both %s and %s", target, alias, other, f.Name)
				}
				seen[normalized] = f.Name
			}
		}
	}
}

// A real export puts a title, an export stamp and a blank line above its
// header. The header is found rather than assumed.
func TestBindHeader_FindsTheHeaderUnderATitleBlock(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		[]string{"Group Entity Register", "", ""},
		[]string{"Exported 03/09/2026", "", ""},
		[]string{"", "", ""},
		[]string{"Entity Name", "Country of incorporation", "Cost centre"},
		[]string{"Acme Holding AG", "Switzerland", "CC-100"},
	)

	binding, ignored, issues := BindHeader(sheet, TargetEntities)
	require.NotNil(t, binding)
	assert.Equal(t, 4, binding.HeaderRow)
	assert.Equal(t, "Entity Name", binding.Header(FieldName))
	assert.Equal(t, []IgnoredColumn{{Column: "C", Header: "Cost centre"}}, ignored)
	require.Len(t, issues, 1)
	assert.Equal(t, SeverityWarning, issues[0].Severity, "an unknown column is reported, never a refusal")
	assert.Contains(t, issues[0].Message, `C ("Cost centre")`)
}

// A file whose columns say nothing this import knows is refused as a file: there
// is no row-by-row report worth giving.
func TestBindHeader_RefusesAFileWithNoRecognisableHeader(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		[]string{"Reference", "Amount", "Posted"},
		[]string{"INV-1", "100", "2026-01-01"},
	)

	binding, _, issues := BindHeader(sheet, TargetEntities)
	assert.Nil(t, binding)
	require.Len(t, issues, 1)
	assert.True(t, issues[0].IsError())
	assert.Contains(t, issues[0].Message, "no header row found")
	assert.Contains(t, issues[0].Message, `"name"`)
	assert.Contains(t, issues[0].Message, `"country"`)
}

// One field with two columns is refused rather than guessed at.
func TestBindHeader_RefusesAFieldClaimedByTwoColumns(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		[]string{"Entity Name", "Country", "Company name"},
		[]string{"Acme Holding AG", "Switzerland", "Acme"},
	)

	binding, _, issues := BindHeader(sheet, TargetEntities)
	assert.Nil(t, binding)
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0].Message, "A (\"Entity Name\") and C (\"Company name\")")
	assert.Contains(t, issues[0].Message, "rename or remove one")
}

// A missing KEY column refuses the file, and names itself in every spelling
// that would have worked, so the fix does not need a manual. Without it no row
// names a record, so there is nothing to match and nothing to read from.
func TestBindHeader_NamesAMissingKeyColumnAndItsSpellings(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		[]string{"Country", "Legal name"},
		[]string{"Switzerland", "Acme Holding Aktiengesellschaft"},
	)

	binding, _, issues := BindHeader(sheet, TargetEntities)
	assert.Nil(t, binding)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Message, `the "name" column is missing`)
	assert.Contains(t, issues[0].Message, `"entity name"`)
}

// A required column that is not the key is required to CREATE a record, not to
// send a file. A correction sheet naming the entity and the one field being
// corrected is exactly the file a customer should be able to send; the stored
// record answers for everything it does not mention, and the rows that would
// create are refused one by one by the planner (MissingOnCreate).
func TestBindHeader_AcceptsACorrectionSheetWithoutEveryRequiredColumn(t *testing.T) {
	t.Parallel()
	entities := sheetOf(
		[]string{"Entity Name", "Legal name"},
		[]string{"Acme Holding AG", "Acme Holding Aktiengesellschaft"},
	)

	binding, _, issues := BindHeader(entities, TargetEntities)
	require.NotNil(t, binding, "a sheet that is the authority on legal names is a file, not a mistake")
	assert.False(t, binding.Has(FieldCountry))
	for _, issue := range issues {
		assert.False(t, issue.IsError(), issue.Message)
	}

	obligations := sheetOf(
		[]string{"Entity", "Tax type", "VAT number"},
		[]string{"Acme Holding AG", "VAT-RET", "CHE-123.456.789"},
	)

	binding, _, issues = BindHeader(obligations, TargetObligations)
	require.NotNil(t, binding, "the smallest sheet that identifies an obligation and corrects one field")
	assert.False(t, binding.Has(FieldPeriodicity))
	for _, issue := range issues {
		assert.False(t, issue.IsError(), issue.Message)
	}
}

// MissingOnCreate is where the requirement lands instead: one refusal per row
// that would create, naming the column and saying which rows are unaffected.
func TestMissingOnCreate_RefusesOnlyWhatCannotBeWritten(t *testing.T) {
	t.Parallel()
	full := map[string]bool{FieldName: true, FieldCountry: true}
	assert.Empty(t, MissingOnCreate(TargetEntities, 4, full))

	issues := MissingOnCreate(TargetEntities, 4, map[string]bool{FieldName: true})
	require.Len(t, issues, 1)
	assert.True(t, issues[0].IsError())
	assert.Equal(t, 4, issues[0].Row)
	assert.Equal(t, FieldCountry, issues[0].Field)
	assert.Contains(t, issues[0].Message, `no "country" column`)
	assert.Contains(t, issues[0].Message, "create a new entity")

	issues = MissingOnCreate(TargetObligations, 7, map[string]bool{FieldEntity: true, FieldObligationType: true})
	require.Len(t, issues, 1)
	assert.Equal(t, FieldPeriodicity, issues[0].Field)
	assert.Contains(t, issues[0].Message, "create a new entity obligation")
}

func TestColumnLetter(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "A", ColumnLetter(0))
	assert.Equal(t, "Z", ColumnLetter(25))
	assert.Equal(t, "AA", ColumnLetter(26))
	assert.Equal(t, "AZ", ColumnLetter(51))
	assert.Equal(t, "BA", ColumnLetter(52))
	assert.Equal(t, "D12", CellRef(3, 12))
}

func TestCleanCell_RemovesWhatAnExportDragsIn(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Entity Name", CleanCell("\uFEFFEntity Name"), "the byte-order mark on the first cell")
	assert.Equal(t, "Acme GmbH", CleanCell("  Acme\u00A0GmbH \u200B"), "a non-breaking space and a zero-width one")
	assert.Equal(t, "", CleanCell("\u200B\u200D"), "a cell holding only invisibles is empty")
	assert.Equal(t, "Acme  GmbH", CleanCell("Acme  GmbH"), "the inside of a value is left alone")
}

// The natural key folds what a person would call the same name, and nothing
// more.
func TestNormalizeKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, NormalizeKey("Acme  GmbH "), NormalizeKey("ACME GmbH"))
	assert.Equal(t, NormalizeKey("acme gmbh"), NormalizeKey("Acme GmbH"))
	assert.NotEqual(t, NormalizeKey("Acme GmbH"), NormalizeKey("Acme GmbH AG"))
}
