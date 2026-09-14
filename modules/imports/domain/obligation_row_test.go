package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var obligationHeader = []string{
	"Entity", "Obligation type", "Tax reference number", "Jurisdiction", "State",
	"Currency", "Filing frequency", "Deadline type", "Period start",
	"Filing offset months", "Filing offset days",
	"Payment offset months", "Payment offset days",
	"Fixed dates", "Payment dates", "Weekend adjustment",
}

func readObligations(t *testing.T, rows ...[]string) *ReadOutcome {
	t.Helper()
	outcome, err := Read(sheetOf(append([][]string{obligationHeader}, rows...)...), TargetObligations, 0)
	require.NoError(t, err)
	return outcome
}

// obligationRow fills the header's width so a test can write only the cells it
// cares about.
func obligationRow(values map[string]string) []string {
	row := make([]string, len(obligationHeader))
	for i, header := range obligationHeader {
		row[i] = values[header]
	}
	return row
}

// The offset strategy: a delay after each period ends, which is how every
// monthly and quarterly filing is actually described.
func TestReadObligation_APeriodOffsetRule(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Deutschland GmbH", "Obligation type": "VAT-RET",
		"Tax reference number": "DE123456789", "Jurisdiction": "Germany",
		"Currency": "eur", "Filing frequency": "Monthly",
		"Filing offset months": "0", "Filing offset days": "10",
		"Payment offset months": "0", "Payment offset days": "10",
		"Weekend adjustment": "next working day",
	}))

	row := firstRow(t, outcome)
	assert.Empty(t, messages(row.Issues))
	draft := row.Obligation
	require.NotNil(t, draft)
	assert.Equal(t, "monthly", draft.Periodicity)
	require.NotNil(t, draft.Currency)
	assert.Equal(t, "EUR", *draft.Currency, "a lower-case code is upper-cased, not refused")

	rule := draft.DeadlineRule
	assert.Equal(t, "period_offset", rule.Type)
	assert.Equal(t, "period_end", rule.Reference)
	require.NotNil(t, rule.FilingOffset)
	assert.Equal(t, 0, rule.FilingOffset.Months)
	assert.Equal(t, 10, rule.FilingOffset.Days)
	require.NotNil(t, rule.PaymentOffset)
	assert.Equal(t, 10, rule.PaymentOffset.Days)
	assert.Equal(t, "next-business-day", rule.WeekendAdjustment)

	input := draft.CreateInput("entity-id", "type-id")
	assert.Equal(t, "entity-id", input.EntityID)
	assert.Equal(t, "type-id", input.ObligationTypeID)
	assert.Equal(t, rule, input.DeadlineRule)
}

// The fixed strategy: calendar dates, which is how an annual return is
// described. Several dates live in one cell, separated however the file
// separates them.
func TestReadObligation_AFixedDateRule(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Retail Nordics AB", "Obligation type": "VAT-RET",
		"Currency": "SEK", "Filing frequency": "bi-annual",
		"Fixed dates": "26 August; 26 February", "Payment dates": "08-26;02-26",
		"Period start": "01-01",
	}))

	row := firstRow(t, outcome)
	assert.Empty(t, messages(row.Issues))
	rule := row.Obligation.DeadlineRule
	assert.Equal(t, "fixed", rule.Type)
	assert.Equal(t, []string{"08-26", "02-26"}, rule.FixedDates)
	assert.Equal(t, []string{"08-26", "02-26"}, rule.PaymentFixedDates)
	require.NotNil(t, rule.PeriodStart)
	assert.Equal(t, 1, rule.PeriodStart.Month)
	assert.Equal(t, 1, rule.PeriodStart.Day)
}

// A row that fills both strategies has not said which it means. The API takes a
// rule that has already chosen; a flat row has to choose too.
func TestReadObligation_RefusesTwoStrategiesWithNoDeclaredWinner(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme France SAS", "Obligation type": "VAT-RET", "Filing frequency": "quarterly",
		"Fixed dates": "04-30;07-31", "Filing offset months": "1",
	}))

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "two different ways to compute a deadline")
	assert.Contains(t, row.Issues[0].Message, `"Fixed dates"`)
	assert.Contains(t, row.Issues[0].Message, `"Filing offset months"`)
	assert.Empty(t, row.Obligation.DeadlineRule.Type)
}

// Declaring the strategy settles it, and the columns the other strategy would
// have used are reported as unread rather than quietly folded in.
func TestReadObligation_ADeclaredStrategySettlesIt(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme France SAS", "Obligation type": "VAT-RET", "Filing frequency": "quarterly",
		"Deadline type": "fixed", "Fixed dates": "04-30;07-31;10-31;01-31", "Filing offset months": "1",
	}))

	row := firstRow(t, outcome)
	assert.False(t, row.HasError())
	require.Len(t, row.Issues, 1)
	assert.Equal(t, SeverityWarning, row.Issues[0].Severity)
	assert.Contains(t, row.Issues[0].Message, "was not read")
	assert.Equal(t, "fixed", row.Obligation.DeadlineRule.Type)
	assert.Nil(t, row.Obligation.DeadlineRule.FilingOffset)
}

// A strategy declared without the values it is made of is refused: it would
// store a rule that computes nothing.
func TestReadObligation_RefusesADeclaredStrategyWithNothingInIt(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme France SAS", "Obligation type": "VAT-RET", "Filing frequency": "quarterly",
		"Deadline type": "period_offset",
	}))

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "no filing offset after period end")
}

// Payment dates pair with filing dates by position, so a count that does not
// match has no meaning.
func TestReadObligation_RefusesPaymentDatesThatDoNotPair(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Retail Nordics AB", "Obligation type": "VAT-RET", "Filing frequency": "bi-annual",
		"Fixed dates": "08-26;02-26", "Payment dates": "08-31",
	}))

	row := firstRow(t, outcome)
	require.NotEmpty(t, row.Issues)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "1 payment date against 2 filing dates")
}

// An obligation with no deadline configuration is recordable — the API's own
// column defaults to an empty rule — so the row reads cleanly here and raises
// nothing. WHAT to say about it is the planner's call, because the answer is a
// different sentence for a new obligation, for one whose filing calendar this
// row leaves exactly as it is, and for one that never had a calendar; see
// usecases.deadlineRemarks.
func TestReadObligation_ReadsARowWithNoDeadlineRuleWithoutComplaint(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Holding AG", "Obligation type": "CIT-ANN", "Filing frequency": "annual",
	}))

	row := firstRow(t, outcome)
	assert.False(t, row.HasError())
	assert.Empty(t, row.Issues)
	assert.Empty(t, row.Obligation.DeadlineRule.Type, "and no rule was invented for it")
}

// Weekly is accepted and says nothing further. It used to carry a warning that
// ZenTax could not generate weekly periods, which is false: the engine produces
// 52 or 53 of them on the standard calendar and on every week-based pattern,
// and only a CUSTOM calendar refuses — which refuses every periodicity, not
// this one. A warning that tells a customer on the default calendar their
// weekly filings cannot be scheduled is worse than no warning.
func TestReadObligation_RecordsWeeklyWithoutWarningAboutIt(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Holding AG", "Obligation type": "VAT-RET", "Filing frequency": "weekly",
		"Filing offset days": "7",
	}))

	row := firstRow(t, outcome)
	assert.False(t, row.HasError())
	assert.Equal(t, "weekly", row.Obligation.Periodicity)
	assert.Empty(t, row.Issues)
}

// A count outside the deadline rule's own validate tag is refused by that tag's
// own numbers.
func TestReadObligation_HoldsOffsetsToTheRulesOwnBounds(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Holding AG", "Obligation type": "VAT-RET", "Filing frequency": "monthly",
		"Filing offset months": "36", "Filing offset days": "half a month",
	}))

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 2, "a row that tried to state a rule is not also told it has none")
	assert.Contains(t, row.Issues[0].Message, "between 0 and 24 months")
	assert.Contains(t, row.Issues[1].Message, "whole number of days")
}

func TestReadObligation_RefusesAnythingThatIsNotACurrencyCode(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Holding AG", "Obligation type": "CIT-ANN", "Filing frequency": "annual",
		"Currency": "Swiss Francs", "Fixed dates": "09-30",
	}))

	row := firstRow(t, outcome)
	require.Len(t, row.Issues, 1)
	assert.True(t, row.Issues[0].IsError())
	assert.Contains(t, row.Issues[0].Message, "three-letter ISO 4217 code")
	assert.Nil(t, row.Obligation.Currency)
}

// The pairing (entity, obligation type) is the natural key, and it is the
// pairing the product has always treated as unique.
func TestRead_RefusesTheSameObligationTwice(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t,
		obligationRow(map[string]string{
			"Entity": "Acme Deutschland GmbH", "Obligation type": "VAT-RET",
			"Filing frequency": "monthly", "Filing offset days": "10",
		}),
		obligationRow(map[string]string{
			"Entity": "acme deutschland gmbh", "Obligation type": " vat-ret ",
			"Filing frequency": "quarterly", "Filing offset days": "10",
		}),
	)

	require.Len(t, outcome.Rows, 2)
	assert.False(t, outcome.Rows[0].HasError())
	require.Len(t, outcome.Rows[1].Issues, 1)
	assert.Contains(t, outcome.Rows[1].Issues[0].Message, "row 2 already gives")
	assert.Contains(t, outcome.Rows[1].Issues[0].Message, `"Acme Deutschland GmbH"`)
	assert.Equal(t, outcome.Rows[0].NaturalKey(), outcome.Rows[1].NaturalKey())
}

// An obligation row's entity key is the same key an entity row produces, which
// is what lets the two files be imported one after the other.
func TestObligationDraft_KeysMatchTheEntityDrafts(t *testing.T) {
	t.Parallel()
	entity := EntityDraft{Name: "Acme  Deutschland GmbH"}
	obligation := ObligationDraft{EntityRef: "ACME DEUTSCHLAND GMBH", ObligationTypeRef: "VAT-RET"}
	assert.Equal(t, entity.NaturalKey(), obligation.EntityKey())
	assert.Equal(t, entity.NaturalKey()+"\x1f"+NormalizeKey("VAT-RET"), obligation.NaturalKey())
}

// A count of fixed dates that does not suit the periodicity is a remark: the
// route would accept it, so this import must too.
func TestReadObligation_RemarksOnAnUnusualNumberOfFixedDates(t *testing.T) {
	t.Parallel()
	outcome := readObligations(t, obligationRow(map[string]string{
		"Entity": "Acme Holding AG", "Obligation type": "CIT-ANN", "Filing frequency": "annual",
		"Fixed dates": "03-31;09-30",
	}))

	row := firstRow(t, outcome)
	assert.False(t, row.HasError())
	require.Len(t, row.Issues, 1)
	assert.Equal(t, SeverityWarning, row.Issues[0].Severity)
	assert.Contains(t, row.Issues[0].Message, "usually has 1 fixed filing date")
}
