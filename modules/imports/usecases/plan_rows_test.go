package usecases

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// The planner against whole rows as the reader hands them over, rather than
// against the pure comparisons next door: what a customer is TOLD about a row
// is decided by the two halves together, and the defects worth a test here are
// the ones that only appear when both have spoken — one mistake reported twice,
// and a required column refused before anyone asked whether the row creates
// anything.

// stubRepo is the tenant as a plan sees it: three lookups and nothing else. The
// writing methods are never reached from planning, and say so by returning
// nothing rather than by pretending to write.
type stubRepo struct {
	entities    map[string]domain.ExistingEntity
	typeIDs     map[string]string
	obligations map[string]domain.ExistingObligation
}

func (r stubRepo) EntitiesByKey(_ context.Context, _ []string) (map[string]domain.ExistingEntity, error) {
	if r.entities == nil {
		return map[string]domain.ExistingEntity{}, nil
	}
	return r.entities, nil
}

func (r stubRepo) ObligationTypeIDsByKey(_ context.Context, _ []string) (map[string]string, error) {
	if r.typeIDs == nil {
		return map[string]string{}, nil
	}
	return r.typeIDs, nil
}

func (r stubRepo) ObligationsByPair(_ context.Context, _ []domain.ObligationPair) (map[string]domain.ExistingObligation, error) {
	if r.obligations == nil {
		return map[string]domain.ExistingObligation{}, nil
	}
	return r.obligations, nil
}

func (r stubRepo) CreateEntity(context.Context, *domain.EntityDraft, *string) (*domain.ExistingEntity, error) {
	return nil, nil
}

func (r stubRepo) UpdateEntity(context.Context, string, time.Time, *domain.EntityDraft, *string) (*domain.ExistingEntity, error) {
	return nil, nil
}

func (r stubRepo) CreateObligation(context.Context, *domain.ObligationDraft, string, string) (*domain.ExistingObligation, error) {
	return nil, nil
}

func (r stubRepo) UpdateObligation(context.Context, string, time.Time, *domain.ObligationDraft) (*domain.ExistingObligation, error) {
	return nil, nil
}

func (r stubRepo) CreateBatch(context.Context, *domain.Batch, []domain.RowPlan) (*domain.Batch, error) {
	return nil, nil
}
func (r stubRepo) GetBatch(context.Context, string) (*domain.Batch, error) { return nil, nil }
func (r stubRepo) GetBatchForUpdate(context.Context, string) (*domain.Batch, error) {
	return nil, nil
}

func (r stubRepo) ListBatches(context.Context, domain.Kind, int, int) ([]domain.Batch, int, error) {
	return nil, 0, nil
}

func (r stubRepo) ListRows(context.Context, string, int, int) ([]domain.RowPlan, int, error) {
	return nil, 0, nil
}
func (r stubRepo) MarkCommitted(context.Context, string) (*domain.Batch, error) { return nil, nil }

// planSheet reads a sheet the way an upload does and plans it against a tenant,
// so a test can assert on what the customer is actually shown.
func planSheet(t *testing.T, repo stubRepo, target domain.Target, rows ...[]string) *domain.BatchPlan {
	t.Helper()
	sheet := domain.Sheet{Name: "Sheet1", Format: domain.FormatCSV}
	for i, cells := range rows {
		sheet.Rows = append(sheet.Rows, domain.Row{Number: i + 1, Cells: cells})
	}
	outcome, err := domain.Read(sheet, target, 0)
	require.NoError(t, err)
	require.False(t, outcome.HasFileError(), "the file itself must be readable: %v", outcome.FileIssues)

	uc := NewUseCases(repo, nil)
	plan, _, err := uc.planFile(context.Background(), domain.KindOf(target), outcome.Rows)
	require.NoError(t, err)
	return plan
}

func rowByNumber(t *testing.T, plan *domain.BatchPlan, number int) domain.RowPlan {
	t.Helper()
	for _, row := range plan.Rows {
		if row.RowNumber == number {
			return row
		}
	}
	t.Fatalf("row %d is not in the plan", number)
	return domain.RowPlan{}
}

func errorIssues(row domain.RowPlan) []domain.Issue {
	var out []domain.Issue
	for _, issue := range row.RowIssues() {
		if issue.IsError() {
			out = append(out, issue)
		}
	}
	return out
}

// One repeated name is ONE error: the reader's, which names the cell, quotes
// the name and says what to do about it. The planner still refuses the row —
// the file may not name an entity twice — but a customer fixing a register must
// not see twice as many errors as they have mistakes, with the second copy the
// one they cannot act on.
func TestDuplicateEntityRowIsReportedOnce(t *testing.T) {
	t.Parallel()

	plan := planSheet(t, stubRepo{}, domain.TargetEntities,
		[]string{"Entity Name", "Country"},
		[]string{"Müller Holding", "Germany"},
		[]string{"Müller Holding", "Germany"},
	)

	repeated := rowByNumber(t, plan, 3)
	require.Equal(t, domain.RowInvalid, repeated.Action, "the row is still refused")

	errs := errorIssues(repeated)
	require.Len(t, errs, 1, "one mistake, one error: %v", errs)
	assert.Equal(t, "A3", errs[0].CellRef, "and it is the one that can be clicked on")
	assert.Contains(t, errs[0].Message, "row 2 already describes this entity")
	assert.Contains(t, errs[0].Message, "merge them")
}

func TestDuplicateObligationRowIsReportedOnce(t *testing.T) {
	t.Parallel()

	plan := planSheet(t, stubRepo{}, domain.TargetObligations,
		[]string{"Entity", "Obligation Type", "Periodicity"},
		[]string{"Müller Deutschland", "VAT-RET", "monthly"},
		[]string{"Müller Deutschland", "VAT-RET", "monthly"},
	)

	repeated := rowByNumber(t, plan, 3)
	require.Equal(t, domain.RowInvalid, repeated.Action)

	errs := errorIssues(repeated)
	require.Len(t, errs, 1, "one mistake, one error: %v", errs)
	assert.Equal(t, "B3", errs[0].CellRef)
	assert.Contains(t, errs[0].Message, "row 2 already gives")
}

// Two rows that name no entity and no obligation type collide on a key made of
// two empty halves. The reader cannot call that a duplicate — there is nothing
// to quote — and the row is already refused for the cells that ARE empty, so
// the planner adds nothing: "row 2 already imports this entity's " is not a
// sentence to show anybody.
func TestARowWithNoKeyIsNotAlsoReportedAsADuplicate(t *testing.T) {
	t.Parallel()

	plan := planSheet(t, stubRepo{}, domain.TargetObligations,
		[]string{"Entity", "Obligation Type", "Periodicity"},
		[]string{"", "", "monthly"},
		[]string{"", "", "quarterly"},
	)

	for _, number := range []int{2, 3} {
		row := rowByNumber(t, plan, number)
		require.Equal(t, domain.RowInvalid, row.Action)
		for _, issue := range row.RowIssues() {
			assert.NotContains(t, issue.Message, "already imports this entity's",
				"row %d", number)
		}
	}
}

// A correction sheet carrying the key and the one column being corrected is a
// file, not a mistake. The rows that match a record are corrected; only a row
// that would CREATE a record is refused, because that is the only row whose
// missing column cannot be answered by the record it lands on.
func TestARequiredColumnRefusesOnlyTheRowsThatCreate(t *testing.T) {
	t.Parallel()

	repo := stubRepo{entities: map[string]domain.ExistingEntity{
		"meridian uk trading": {
			ID: "e-1", Name: "Meridian UK Trading", Country: "United Kingdom",
			LegalName: strptr("Meridian UK Limited"),
			Pattern:   "445", WeekEndDay: "saturday", YearEndRule: "nearest",
		},
	}}

	plan := planSheet(t, repo, domain.TargetEntities,
		[]string{"Entity Name", "Legal name"},
		[]string{"Meridian UK Trading", "Meridian UK Holdings Ltd"},
		[]string{"Meridian Ireland DAC", "Meridian Ireland Designated Activity Company"},
	)

	corrected := rowByNumber(t, plan, 2)
	require.Equal(t, domain.RowUpdate, corrected.Action, "the sheet corrects what it names")
	require.Equal(t, []domain.FieldChange{{
		Field: "legalName", From: strptr("Meridian UK Limited"), To: strptr("Meridian UK Holdings Ltd"),
	}}, corrected.Changes, "and proposes nothing about the country it never mentioned")
	assert.Empty(t, errorIssues(corrected))

	created := rowByNumber(t, plan, 3)
	require.Equal(t, domain.RowInvalid, created.Action)
	errs := errorIssues(created)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.FieldCountry, errs[0].Field)
	assert.Contains(t, errs[0].Message, "create a new entity")
	assert.Contains(t, errs[0].Message, `no "country" column`)
	assert.Contains(t, errs[0].Message, "not affected")
}

// The same rule for an obligation: the minimal sheet that corrects one VAT
// number does not have to restate the filing frequency, and a guessed frequency
// is exactly what the old refusal was pushing a customer into typing.
func TestAnObligationCorrectionNeedNotRestateThePeriodicity(t *testing.T) {
	t.Parallel()

	repo := stubRepo{
		entities: map[string]domain.ExistingEntity{
			"meridian uk trading": {ID: "e-1", Name: "Meridian UK Trading"},
		},
		typeIDs: map[string]string{"vat-ret": "t-1", "cit-ann": "t-2"},
		obligations: map[string]domain.ExistingObligation{
			"meridian uk trading\x1fvat-ret": {
				ID: "o-1", EntityID: "e-1", ObligationTypeID: "t-1",
				Periodicity: "monthly", TaxReferenceNumber: strptr("GB998877665"),
				DeadlineRule: []byte(`{"type":"period_offset","reference":"period_end",` +
					`"filingOffset":{"months":1,"days":7}}`),
			},
		},
	}

	plan := planSheet(t, repo, domain.TargetObligations,
		[]string{"Entity", "Tax type", "VAT number"},
		[]string{"Meridian UK Trading", "VAT-RET", "GB998877666"},
		[]string{"Meridian UK Trading", "CIT-ANN", "GB111222333"},
	)

	corrected := rowByNumber(t, plan, 2)
	require.Equal(t, domain.RowUpdate, corrected.Action)
	require.Equal(t, []domain.FieldChange{{
		Field: "taxReferenceNumber", From: strptr("GB998877665"), To: strptr("GB998877666"),
	}}, corrected.Changes, "the filing frequency is not restated, so it does not move")
	assert.Empty(t, errorIssues(corrected))

	created := rowByNumber(t, plan, 3)
	require.Equal(t, domain.RowInvalid, created.Action)
	errs := errorIssues(created)
	require.Len(t, errs, 1)
	assert.Equal(t, domain.FieldPeriodicity, errs[0].Field)
	assert.Contains(t, errs[0].Message, "create a new entity obligation")
}

// A name a Mac spelled and a name a browser spelled are one entity: the row
// matches the record that is there, instead of creating a twin nothing on the
// page can tell apart — and it reports no change, because there is none to see.
func TestAnEntityIsMatchedWhateverWayItsNameIsSpelled(t *testing.T) {
	t.Parallel()

	const combining = "̈" // A combining diaeresis, as a macOS export leaves it.
	stored := "Müller Holding"
	fromMac := "Mu" + combining + "ller Holding"
	require.NotEqual(t, stored, fromMac, "the two spellings must really differ in bytes")

	repo := stubRepo{entities: map[string]domain.ExistingEntity{
		domain.NormalizeKey(stored): {
			ID: "e-1", Name: stored, Country: "Germany",
			Pattern: "standard", WeekEndDay: "saturday", YearEndRule: "nearest",
		},
	}}

	plan := planSheet(t, repo, domain.TargetEntities,
		[]string{"Entity Name", "Country"},
		[]string{fromMac, "Germany"},
	)

	row := rowByNumber(t, plan, 2)
	assert.Equal(t, domain.RowUnchanged, row.Action,
		"one entity, however the file spells it")
	assert.Empty(t, row.Changes, "and no change whose two sides read the same on screen")
	assert.Zero(t, plan.CreateCount)
}

// And a name whose spelling folds to another entity's is still that entity's
// row: the parent reference resolves through the same fold, so a group imported
// from two systems does not end up hanging off two different parents.
func TestAParentIsResolvedThroughTheSameFold(t *testing.T) {
	t.Parallel()

	const combining = "̈"
	parent := "Müller Holding"
	repo := stubRepo{entities: map[string]domain.ExistingEntity{
		domain.NormalizeKey(parent): {
			ID: "e-parent", Name: parent, Country: "Germany",
			Pattern: "standard", WeekEndDay: "saturday", YearEndRule: "nearest",
		},
	}}

	plan := planSheet(t, repo, domain.TargetEntities,
		[]string{"Entity Name", "Country", "Parent"},
		[]string{"Müller Deutschland", "Germany", "Mu" + combining + "ller Holding"},
	)

	row := rowByNumber(t, plan, 2)
	require.Equal(t, domain.RowCreate, row.Action)
	for _, issue := range row.RowIssues() {
		assert.False(t, issue.IsError(), issue.Message)
		assert.NotContains(t, issue.Message, "is in this file or in ZenTax")
	}
	assert.False(t, strings.Contains(string(row.Issues), "no entity called"))
}
