package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var entityHeader = []string{
	"Entity Name", "Legal name", "Country of incorporation", "Parent company",
	"Financial Year End (MM-DD)", "Fiscal calendar", "Tax residency",
	"Fiscal week end day", "Year end rule",
}

func readEntities(t *testing.T, rows ...[]string) *ReadOutcome {
	t.Helper()
	outcome, err := Read(sheetOf(append([][]string{entityHeader}, rows...)...), TargetEntities, 0)
	require.NoError(t, err)
	return outcome
}

func firstRow(t *testing.T, outcome *ReadOutcome) DraftRow {
	t.Helper()
	require.Len(t, outcome.Rows, 1)
	return outcome.Rows[0]
}

func messages(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Message)
	}
	return out
}

// A complete row lands as the create contract, with the calendar defaults the
// entity use case would have applied filled in rather than left blank.
func TestReadEntity_ACompleteRow(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{
		"Acme Deutschland GmbH", "Acme Deutschland GmbH", "Germany", "Acme Holding AG",
		"12-31", "4-4-5", "Germany", "Sunday", "last",
	})

	row := firstRow(t, outcome)
	assert.Empty(t, messages(row.Issues))
	require.NotNil(t, row.Entity)
	assert.Equal(t, "Acme Deutschland GmbH", row.Entity.Name)
	assert.Equal(t, "Germany", row.Entity.Country)
	assert.Equal(t, "Acme Holding AG", row.Entity.ParentRef)
	assert.Equal(t, "445", row.Entity.FiscalCalendarPattern)
	require.NotNil(t, row.Entity.FinancialYearEnd)
	assert.Equal(t, "12-31", *row.Entity.FinancialYearEnd)
	assert.Equal(t, "sunday", row.Entity.FiscalWeekEndDay)
	assert.Equal(t, "last", row.Entity.FiscalYearEndRule)

	input := row.Entity.CreateInput(nil)
	assert.Equal(t, "Acme Deutschland GmbH", input.Name)
	assert.Nil(t, input.ParentEntityID, "the parent is resolved by the half that can see the tenant")
	assert.Nil(t, input.CustomPeriods)
}

// An omitted calendar column takes the entity module's own default, not a blank.
func TestReadEntity_AppliesTheModulesOwnDefaults(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{"Acme Holding AG", "", "Switzerland", "", "", "", "", "", ""})

	row := firstRow(t, outcome)
	assert.Empty(t, messages(row.Issues))
	assert.Equal(t, "standard", row.Entity.FiscalCalendarPattern)
	assert.Equal(t, "saturday", row.Entity.FiscalWeekEndDay)
	assert.Equal(t, "nearest", row.Entity.FiscalYearEndRule)
	assert.Nil(t, row.Entity.LegalName, "an empty optional cell is absent, not blank")
	assert.Nil(t, row.Entity.FinancialYearEnd)
	assert.Empty(t, row.Entity.ParentRef)
}

// The one fiscal pattern a flat row cannot carry is refused where the customer
// can act on it, and the refusal says what to do instead.
func TestReadEntity_RefusesACustomCalendar(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{"Acme Holding AG", "", "Switzerland", "", "12-31", "custom", "", "", ""})

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "custom fiscal calendar cannot be described in a spreadsheet row")
	assert.Contains(t, row.Issues[0].Message, "set its custom periods afterwards")
	assert.Equal(t, "F2", row.Issues[0].CellRef)
}

// A row is checked by the module's own rule, not by a copy of it: a financial
// year end that entities/domain.ValidateFiscalConfig would refuse is refused
// here in the same words.
func TestReadEntity_RunsTheEntityModulesFiscalValidation(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{"Acme Holding AG", "", "Switzerland", "", "02-30", "standard", "", "", ""})

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "not a date that exists")
}

func TestReadEntity_RefusesAnEntityThatIsItsOwnParent(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{"Acme Holding AG", "", "Switzerland", "acme  HOLDING ag", "", "", "", "", ""})

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.Contains(t, row.Issues[0].Message, "is named as its own parent")
	assert.Empty(t, row.Entity.ParentRef)
}

// Every problem in a row is reported at once, in the order the columns appear —
// a customer fixes a file in one pass or they fix it five times.
func TestReadEntity_ReportsEveryProblemInARow(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t, []string{"", "", "", "", "31/01/2026", "gregorian calendar", "", "", ""})

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 3)
	assert.Equal(t, "A2", row.Issues[0].CellRef)
	assert.Contains(t, row.Issues[0].Message, `the "Entity Name" cell is empty`)
	assert.Equal(t, "C2", row.Issues[1].CellRef)
	assert.Contains(t, row.Issues[1].Message, `the "Country of incorporation" cell is empty`)
	assert.Equal(t, "F2", row.Issues[2].CellRef)
	assert.Contains(t, row.Issues[2].Message, "is not a fiscal calendar ZenTax knows")
	assert.Contains(t, row.Issues[2].Message, "standard, 445, 454, 544, 13-period, weekly, custom")
}

// The same entity twice in one file has no single meaning — the file's own
// parent column refers to entities by name — so the second is refused, and the
// message quotes the row it collides with.
func TestRead_RefusesTheSameEntityTwice(t *testing.T) {
	t.Parallel()
	outcome := readEntities(t,
		[]string{"Acme Holding AG", "", "Switzerland", "", "", "", "", "", ""},
		[]string{"ACME  holding ag", "", "Switzerland", "", "", "", "", "", ""},
	)

	require.Len(t, outcome.Rows, 2)
	assert.False(t, outcome.Rows[0].HasError())
	require.Len(t, outcome.Rows[1].Issues, 1)
	assert.Contains(t, outcome.Rows[1].Issues[0].Message, "row 2 already describes this entity")
	assert.Contains(t, outcome.Rows[1].Issues[0].Message, `"Acme Holding AG"`)
	assert.Equal(t, outcome.Rows[0].NaturalKey(), outcome.Rows[1].NaturalKey())
}

// A summary line under the table is skipped rather than read as an entity, and
// the skip is named.
func TestRead_SkipsARowWithNothingInAnyImportedColumn(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		append(entityHeader, "Cost centre"),
		append([]string{"Acme Holding AG", "", "Switzerland", "", "", "", "", "", ""}, "CC-100"),
		append([]string{"", "", "", "", "", "", "", "", ""}, "Entities: 1"),
	)

	outcome, err := Read(sheet, TargetEntities, 0)
	require.NoError(t, err)
	require.Len(t, outcome.Rows, 1)
	assert.Equal(t, 2, outcome.Rows[0].Number)

	var skipped string
	for _, issue := range outcome.FileIssues {
		if !issue.IsError() && issue.Row == 0 && strings.Contains(issue.Message, "skipped") {
			skipped = issue.Message
		}
	}
	assert.Contains(t, skipped, "row 3")
}

// A blank line in the middle of a file shifts nothing: row 5 in a message is
// row 5 in the customer's spreadsheet.
func TestRead_KeepsTheCustomersRowNumbersAcrossBlankLines(t *testing.T) {
	t.Parallel()
	sheet := sheetOf(
		entityHeader,
		[]string{"Acme Holding AG", "", "Switzerland", "", "", "", "", "", ""},
		[]string{},
		[]string{"Acme France SAS", "", "France", "Acme Holding AG", "", "", "", "", ""},
	)

	outcome, err := Read(sheet, TargetEntities, 0)
	require.NoError(t, err)
	require.Len(t, outcome.Rows, 2)
	assert.Equal(t, 2, outcome.Rows[0].Number)
	assert.Equal(t, 4, outcome.Rows[1].Number)
}

// Past the row cap the file is refused outright rather than half-reported: a
// report that silently stops at row 10,000 is a report that lies.
func TestRead_RefusesAFileLongerThanOneImportMayCarry(t *testing.T) {
	t.Parallel()
	rows := [][]string{entityHeader}
	for i := 0; i < 5; i++ {
		rows = append(rows, []string{"Entity " + string(rune('A'+i)), "", "Switzerland", "", "", "", "", "", ""})
	}

	outcome, err := Read(sheetOf(rows...), TargetEntities, 3)
	assert.Nil(t, outcome)
	require.ErrorIs(t, err, ErrTooManyRows)
}

func TestRead_RefusesATargetThisWaveDoesNotImport(t *testing.T) {
	t.Parallel()
	outcome, err := Read(sheetOf(entityHeader), "workflows", 0)
	require.NoError(t, err)
	require.True(t, outcome.HasFileError())
	assert.Contains(t, outcome.FileIssues[0].Message, "not something ZenTax imports")
	assert.Contains(t, outcome.FileIssues[0].Message, "Workflows and task instances are generated")
}
