package usecases

import (
	"testing"

	"github.com/stretchr/testify/require"

	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// The pure pieces of the planner, at the level the acceptance suite can only
// reach through a whole file: which rows a parent loop makes unwritable, the
// order the writes have to go in, which fields an update actually moves and
// what it leaves alone, and the sentences a customer is shown about both.

func TestEntityCyclesFindsLoopsAndWhatLeadsIntoThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		parentOf map[string]string
		existing map[string]domain.ExistingEntity
		wantRows []int
	}{
		{
			name:     "a tree writes in some order, so nothing is refused",
			parentOf: map[string]string{"group": "", "sub a": "group", "sub b": "group"},
		},
		{
			name:     "two rows naming each other cannot be written at all",
			parentOf: map[string]string{"one": "two", "two": "one"},
			wantRows: []int{1, 2},
		},
		{
			name:     "a longer loop, and the row that hangs off it",
			parentOf: map[string]string{"a": "b", "b": "c", "c": "a", "d": "a"},
			wantRows: []int{1, 2, 3, 4},
		},
		{
			name:     "a parent that already exists is a fixed point, never a loop",
			parentOf: map[string]string{"sub": "group"},
			existing: map[string]domain.ExistingEntity{"group": {ID: "e-1"}},
		},
		{
			name: "a parent this file does not carry is not an in-file edge",
			// The planner refuses such a row separately, by name; it is not a
			// cycle and must not be reported as one.
			parentOf: map[string]string{"orphan": "somebody else"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := entityCycles(tc.parentOf, inFileNumbered(tc.parentOf), tc.existing)
			require.Len(t, got, len(tc.wantRows),
				"rows caught in a parent loop: got %v", got)
		})
	}
}

// inFileNumbered gives every key a stable row number (1..n) in sorted order, so
// a test can reason about how many rows were marked without depending on map
// iteration order.
func inFileNumbered(parentOf map[string]string) map[string]int {
	keys := make([]string, 0, len(parentOf))
	for key := range parentOf {
		keys = append(keys, key)
	}
	sortStrings(keys)
	out := make(map[string]int, len(keys))
	for i, key := range keys {
		out[key] = i + 1
	}
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func TestEntityApplyOrderPutsParentsFirst(t *testing.T) {
	t.Parallel()

	// The file lists a grandchild, then a child, then the root — the order a
	// customer's spreadsheet is least likely to be in and the one a naive
	// commit would fail on.
	drafts := map[int]*domain.EntityDraft{
		2: {Name: "Deep Sub Ltd", ParentRef: "Mid Ltd"},
		3: {Name: "Mid Ltd", ParentRef: "Root Ltd"},
		4: {Name: "Root Ltd"},
	}
	plan := []domain.RowPlan{
		{RowNumber: 2, Action: domain.RowCreate},
		{RowNumber: 3, Action: domain.RowCreate},
		{RowNumber: 4, Action: domain.RowCreate},
	}

	order := entityApplyOrder(plan, drafts)
	require.Len(t, order, 3)

	position := map[string]int{}
	for at, index := range order {
		position[drafts[plan[index].RowNumber].Name] = at
	}
	require.Less(t, position["Root Ltd"], position["Mid Ltd"], "the root is written first")
	require.Less(t, position["Mid Ltd"], position["Deep Sub Ltd"], "then its child")
}

func TestEntityApplyOrderTerminatesOnALoop(t *testing.T) {
	t.Parallel()

	// The planner refuses such a file, so this order is never used — but the
	// walk must still terminate and still emit every row, because a function
	// that hangs on bad input is a worse failure than one that returns it.
	drafts := map[int]*domain.EntityDraft{
		2: {Name: "One", ParentRef: "Two"},
		3: {Name: "Two", ParentRef: "One"},
	}
	plan := []domain.RowPlan{{RowNumber: 2}, {RowNumber: 3}}

	require.Len(t, entityApplyOrder(plan, drafts), 2)
}

func TestEntityChangesNamesOnlyWhatMoves(t *testing.T) {
	t.Parallel()

	parentName := "Acme Group"
	parent := "e-parent"
	target := domain.ExistingEntity{
		ID: "e-1", Name: "Acme GmbH", Country: "Germany",
		Pattern: "standard", WeekEndDay: "saturday", YearEndRule: "nearest",
		YearEnd: strptr("12-31"), ParentID: &parent, ParentName: &parentName,
	}
	draft := &domain.EntityDraft{
		Name: "Acme GmbH", Country: "Germany",
		FiscalCalendarPattern: "standard", FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
		FinancialYearEnd: strptr("12-31"), ParentRef: "Acme Group",
		Spoke: everyEntityColumn(),
	}

	require.Empty(t, entityChanges(draft, &parent, "acme group", target),
		"a file that agrees with the record changes nothing")

	moved := *draft
	moved.Country = "Austria"
	require.Equal(t,
		[]domain.FieldChange{{Field: "country", From: strptr("Germany"), To: strptr("Austria")}},
		entityChanges(&moved, &parent, "acme group", target))

	// A parent this file is about to create counts as a change: the entity is
	// going to hang off a record that does not exist yet.
	require.Equal(t,
		[]domain.FieldChange{{Field: "parent", From: &parentName, To: strptr("Acme Group")}},
		entityChanges(draft, nil, "some new group", target))

	// And a blank parent column means "top level", which for a record that has
	// a parent today is also a change — shown as the parent it is losing.
	topLevel := *draft
	topLevel.ParentRef = ""
	require.Equal(t,
		[]domain.FieldChange{{Field: "parent", From: &parentName, To: nil}},
		entityChanges(&topLevel, nil, "", target))
}

// TestEntityChangesLeavesAloneWhatTheFileHasNoColumnFor is the merge rule at
// its narrowest: a two-column sheet is the authority on two columns.
func TestEntityChangesLeavesAloneWhatTheFileHasNoColumnFor(t *testing.T) {
	t.Parallel()

	parentName := "Meridian Group"
	parent := "e-parent"
	target := domain.ExistingEntity{
		ID: "e-1", Name: "Meridian UK Trading", Country: "United Kingdom",
		LegalName: strptr("Meridian United Kingdom Trading Limited"), TaxResidency: strptr("United Kingdom"),
		Pattern: "445", WeekEndDay: "saturday", YearEndRule: "nearest",
		YearEnd: strptr("03-31"), ParentID: &parent, ParentName: &parentName,
	}
	// What a name-and-country export produces: two columns, and the reader's
	// documented defaults in every other field of the draft.
	draft := &domain.EntityDraft{
		Name: "Meridian UK Trading", Country: "United Kingdom",
		FiscalCalendarPattern: "standard", FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
		Spoke: spoke(domain.FieldName, domain.FieldCountry),
	}

	require.Empty(t, entityChanges(draft, nil, "", target),
		"a file that mentions name and country proposes nothing about anything else")

	// And what it WRITES is the record as it stands, field for field.
	merged := mergeEntity(draft, target)
	require.Equal(t, "445", merged.FiscalCalendarPattern)
	require.Equal(t, strptr("03-31"), merged.FinancialYearEnd)
	require.Equal(t, strptr("United Kingdom"), merged.TaxResidency)
	require.Equal(t, strptr("Meridian United Kingdom Trading Limited"), merged.LegalName)
	require.Equal(t, &parent, mergeParent(draft, nil, target), "and it stays where it is in the group")
}

// TestEntityChangesClearsAColumnTheFileIncludedAndLeftEmpty is the other half
// of the ruling: an empty cell in a column the customer chose to include IS a
// statement.
func TestEntityChangesClearsAColumnTheFileIncludedAndLeftEmpty(t *testing.T) {
	t.Parallel()

	target := domain.ExistingEntity{
		ID: "e-1", Name: "Meridian UK Trading", Country: "United Kingdom",
		TaxResidency: strptr("United Kingdom"), Pattern: "445",
		WeekEndDay: "saturday", YearEndRule: "nearest", YearEnd: strptr("03-31"),
	}
	draft := &domain.EntityDraft{
		Name: "Meridian UK Trading", Country: "United Kingdom",
		FiscalCalendarPattern: "standard", FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
		Spoke: spoke(domain.FieldName, domain.FieldCountry, domain.FieldTaxResidency),
	}

	require.Equal(t,
		[]domain.FieldChange{{Field: "taxResidency", From: strptr("United Kingdom"), To: nil}},
		entityChanges(draft, nil, "", target),
		"the empty cell clears the field, and nothing else moves")

	merged := mergeEntity(draft, target)
	require.Nil(t, merged.TaxResidency)
	require.Equal(t, "445", merged.FiscalCalendarPattern, "the calendar was not in the file")
}

func TestObligationChangesComparesTheDeadlineRuleByValue(t *testing.T) {
	t.Parallel()

	target := domain.ExistingObligation{
		ID: "o-1", Periodicity: "monthly", Currency: strptr("EUR"),
		// Key order deliberately differs from what the struct marshals, since
		// Postgres returns jsonb in its own order.
		DeadlineRule: []byte(`{"reference":"period_end","type":"period_offset","filingOffset":{"months":0,"days":20}}`),
	}
	draft := &domain.ObligationDraft{
		Periodicity: "monthly", Currency: strptr("EUR"), Spoke: everyObligationColumn(),
	}
	draft.DeadlineRule.Type = "period_offset"
	draft.DeadlineRule.Reference = "period_end"
	draft.DeadlineRule.FilingOffset = &entityobligationsdomain.MonthDayOffset{Months: 0, Days: 20}

	require.Empty(t, obligationChanges(draft, target),
		"the same rule in a different key order is the same rule")

	moved := *draft
	moved.Periodicity = "quarterly"
	require.Equal(t,
		[]domain.FieldChange{{Field: "periodicity", From: strptr("monthly"), To: strptr("quarterly")}},
		obligationChanges(&moved, target))

	// A rule that does move is shown as the two sentences it computes, not as
	// the JSON that stores it.
	rewritten := *draft
	rewritten.DeadlineRule.FilingOffset = &entityobligationsdomain.MonthDayOffset{Months: 1, Days: 7}
	rewritten.DeadlineRule.WeekendAdjustment = "next-business-day"
	require.Equal(t, []domain.FieldChange{{
		Field: "deadlineType",
		From:  strptr("20 days after period end"),
		To:    strptr("1 month 7 days after period end, weekends move to the next business day"),
	}}, obligationChanges(&rewritten, target))
}

// TestObligationKeepsTheFilingCalendarAFileDoesNotMention: the correction every
// pilot makes second — one row, four columns, a new tax reference number — must
// not take the quarterly filing calendar with it.
func TestObligationKeepsTheFilingCalendarAFileDoesNotMention(t *testing.T) {
	t.Parallel()

	target := domain.ExistingObligation{
		ID: "o-1", Periodicity: "quarterly",
		TaxReferenceNumber: strptr("GB998877665"), Jurisdiction: strptr("United Kingdom"),
		Currency: strptr("GBP"),
		DeadlineRule: []byte(`{"type":"period_offset","reference":"period_end",` +
			`"weekendAdjustment":"next-business-day","filingOffset":{"months":1,"days":7}}`),
	}
	// Entity, Tax type, VAT number, Filing frequency — and nothing else.
	draft := &domain.ObligationDraft{
		EntityRef: "Meridian UK Trading", ObligationTypeRef: "VAT",
		TaxReferenceNumber: strptr("GB998877666"), Periodicity: "quarterly",
		Spoke: spoke(domain.FieldEntity, domain.FieldObligationType,
			domain.FieldTaxReferenceNumber, domain.FieldPeriodicity),
	}

	require.Equal(t, []domain.FieldChange{{
		Field: "taxReferenceNumber", From: strptr("GB998877665"), To: strptr("GB998877666"),
	}}, obligationChanges(draft, target), "one column moved, so one field changes")

	merged, err := mergeObligation(draft, target)
	require.NoError(t, err)
	require.Equal(t, "period_offset", merged.DeadlineRule.Type)
	require.Equal(t, 1, merged.DeadlineRule.FilingOffset.Months)
	require.Equal(t, 7, merged.DeadlineRule.FilingOffset.Days)
	require.Equal(t, "next-business-day", merged.DeadlineRule.WeekendAdjustment)
	require.Equal(t, strptr("United Kingdom"), merged.Jurisdiction)
	require.Equal(t, strptr("GBP"), merged.Currency)

	// And the remark on that row says nothing about deadlines, because nothing
	// happens to them.
	require.Empty(t, deadlineRemarks(2, draft, target, true))
}

// TestARewrittenRuleKeepsThePartsNoColumnOfTheFileTouches: a file that changes
// the filing offset replaces the strategy and its dates — those are read as a
// set — but not the weekend rule it has no column for, and never the extra
// per-period deadlines, which no spreadsheet column can carry at all.
func TestARewrittenRuleKeepsThePartsNoColumnOfTheFileTouches(t *testing.T) {
	t.Parallel()

	target := domain.ExistingObligation{
		ID: "o-1", Periodicity: "monthly",
		DeadlineRule: []byte(`{"type":"period_offset","reference":"period_end",` +
			`"weekendAdjustment":"next-business-day","filingOffset":{"months":1,"days":7},` +
			`"additionalDeadlines":[{"type":"advance payment","months":0,"days":15}]}`),
	}
	// Entity, Obligation type, Frequency, Filing offset days — four columns.
	draft := &domain.ObligationDraft{
		EntityRef: "Acme GmbH", ObligationTypeRef: "VAT-RET", Periodicity: "monthly",
		Spoke: spoke(domain.FieldEntity, domain.FieldObligationType,
			domain.FieldPeriodicity, domain.FieldFilingOffsetDays),
	}
	draft.DeadlineRule.Type = "period_offset"
	draft.DeadlineRule.Reference = "period_end"
	draft.DeadlineRule.FilingOffset = &entityobligationsdomain.MonthDayOffset{Days: 20}

	require.Equal(t, []domain.FieldChange{{
		Field: "deadlineType",
		From: strptr("1 month 7 days after period end," +
			" weekends move to the next business day, 1 extra deadline"),
		To: strptr("20 days after period end, weekends move to the next business day, 1 extra deadline"),
	}}, obligationChanges(draft, target), "and the preview says exactly that")

	merged, err := mergeObligation(draft, target)
	require.NoError(t, err)
	require.Equal(t, 20, merged.DeadlineRule.FilingOffset.Days)
	require.Equal(t, "next-business-day", merged.DeadlineRule.WeekendAdjustment)
	require.Len(t, merged.DeadlineRule.AdditionalDeadlines, 1)
	require.Equal(t, "advance payment", merged.DeadlineRule.AdditionalDeadlines[0].Type)
}

// TestDeadlineRemarkFitsWhatTheRowWillDo: the same row, against three different
// worlds, is three different statements.
func TestDeadlineRemarkFitsWhatTheRowWillDo(t *testing.T) {
	t.Parallel()

	draft := &domain.ObligationDraft{
		EntityRef: "Meridian UK Trading", ObligationTypeRef: "VAT", Periodicity: "quarterly",
		Spoke: spoke(domain.FieldEntity, domain.FieldObligationType, domain.FieldPeriodicity),
	}

	newRecord := deadlineRemarks(2, draft, domain.ExistingObligation{}, false)
	require.Len(t, newRecord, 1)
	require.Equal(t, domain.SeverityWarning, newRecord[0].Severity)
	require.Contains(t, newRecord[0].Message, "will be recorded")

	noRuleEither := deadlineRemarks(2, draft, domain.ExistingObligation{ID: "o-1"}, true)
	require.Len(t, noRuleEither, 1)
	require.Contains(t, noRuleEither[0].Message, "none in ZenTax either")

	// A row that filled one deadline cell without describing a rule: the stored
	// rule stands, and the row is told its cells were not applied.
	partial := *draft
	partial.DeadlineRule.WeekendAdjustment = "next-business-day"
	kept := deadlineRemarks(2, &partial, domain.ExistingObligation{
		ID:           "o-1",
		DeadlineRule: []byte(`{"type":"fixed","fixedDates":["07-31","01-31"]}`),
	}, true)
	require.Len(t, kept, 1)
	require.Contains(t, kept[0].Message, "fixed filing dates 07-31, 01-31")
	require.Contains(t, kept[0].Message, "is kept")
}

// TestStalePlanRefusalReadsAsASentence: the message a customer meets at the one
// moment they are already worried about whether anything was written.
func TestStalePlanRefusalReadsAsASentence(t *testing.T) {
	t.Parallel()

	stored := []domain.RowPlan{{RowNumber: 2, Action: domain.RowCreate}}
	rederived := []domain.RowPlan{{RowNumber: 2, Action: domain.RowUnchanged}}

	err := planStillHolds(stored, rederived)
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"it would now leave the record it matches unchanged rather than add a new record")
	require.Contains(t, err.Error(), "Nothing was imported")
}

func strptr(s string) *string { return &s }

// spoke is the set of fields a file had a column for, as the reader records it
// on every draft.
func spoke(fields ...string) map[string]bool {
	out := make(map[string]bool, len(fields))
	for _, field := range fields {
		out[field] = true
	}
	return out
}

func everyEntityColumn() map[string]bool     { return everyColumn(domain.TargetEntities) }
func everyObligationColumn() map[string]bool { return everyColumn(domain.TargetObligations) }

func everyColumn(target domain.Target) map[string]bool {
	out := map[string]bool{}
	for _, field := range domain.Fields(target) {
		out[field.Name] = true
	}
	return out
}
