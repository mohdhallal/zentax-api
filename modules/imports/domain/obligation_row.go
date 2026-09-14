package domain

import (
	"errors"
	"strings"

	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// The bounds are entityobligations/dto.CreateEntityObligationBody's own
// validate tags, and the offset bounds are DeadlineRule.MonthDayOffset's.
const (
	obligationRefMaxLen     = 200
	taxReferenceMaxLen      = 100
	jurisdictionMaxLen      = 100
	jurisdictionStateMaxLen = 100
	currencyLen             = 3
	offsetMonthsMax         = 24
	offsetDaysMax           = 366

	deadlineTypeFixed  = "fixed"
	deadlineTypeOffset = "period_offset"
	referencePeriodEnd = "period_end"
)

// ObligationDraft is one validated entity-obligation row: the create contract
// with the two references still written the way the file writes them.
//
// AdditionalDeadlines are not importable — an unbounded list of named extra
// deadlines is not a thing a flat row carries — and neither is a status: an
// import records the obligation, it does not retire one.
type ObligationDraft struct {
	// EntityRef and ObligationTypeRef are the entity's name and the obligation
	// type's code or name, as the file writes them. Resolving them to ids is
	// the committing half's job; it is the only half that can see the tenant.
	EntityRef          string  `json:"entityRef"`
	ObligationTypeRef  string  `json:"obligationTypeRef"`
	TaxReferenceNumber *string `json:"taxReferenceNumber,omitempty"`
	Jurisdiction       *string `json:"jurisdiction,omitempty"`
	JurisdictionState  *string `json:"jurisdictionState,omitempty"`
	Currency           *string `json:"currency,omitempty"`
	Periodicity        string  `json:"periodicity"`

	DeadlineRule entityobligationsdomain.DeadlineRule `json:"deadlineRule"`

	// Spoke names the fields the file had a column for — see EntityDraft.Spoke
	// for why an absent column and an empty cell must not be the same thing.
	Spoke map[string]bool `json:"spoke,omitempty"`
}

// Said reports whether the file had a column for this field.
func (d ObligationDraft) Said(field string) bool { return d.Spoke[field] }

// EntityKey is the natural key of the entity this row hangs off — the same key
// EntityDraft.NaturalKey produces, so an obligation file and an entity file
// agree about which entity they mean.
func (d ObligationDraft) EntityKey() string { return NormalizeKey(d.EntityRef) }

// ObligationTypeKey is the folded obligation-type reference.
func (d ObligationDraft) ObligationTypeKey() string { return NormalizeKey(d.ObligationTypeRef) }

// NaturalKey is what a second import of the same file matches this obligation
// on: the pairing of entity and obligation type, which the product has always
// treated as unique (seed/demo/spec: "Exactly one may exist per (entity,
// obligationType)").
func (d ObligationDraft) NaturalKey() string {
	return d.EntityKey() + "\x1f" + d.ObligationTypeKey()
}

// CreateInput is the draft as the entity-obligation use case takes it, with the
// two references resolved to ids by the caller.
func (d ObligationDraft) CreateInput(entityID, obligationTypeID string) entityobligationsdomain.CreateEntityObligationInput {
	return entityobligationsdomain.CreateEntityObligationInput{
		EntityID:           entityID,
		ObligationTypeID:   obligationTypeID,
		TaxReferenceNumber: d.TaxReferenceNumber,
		Jurisdiction:       d.Jurisdiction,
		JurisdictionState:  d.JurisdictionState,
		Currency:           d.Currency,
		Periodicity:        d.Periodicity,
		DeadlineRule:       d.DeadlineRule,
	}
}

// readObligationRow reads and validates one entity-obligation row, reporting
// every problem it finds rather than stopping at the first.
func readObligationRow(b *Binding, row Row) (*ObligationDraft, []Issue) {
	var issues []Issue
	add := func(i Issue) { issues = append(issues, i) }

	draft := &ObligationDraft{Spoke: spokenFields(b, TargetObligations)}
	draft.EntityRef = readRequiredText(b, row, TargetObligations, FieldEntity, 1, obligationRefMaxLen, add)
	draft.ObligationTypeRef = readRequiredText(b, row, TargetObligations, FieldObligationType, 1, obligationRefMaxLen, add)
	draft.TaxReferenceNumber = readOptionalText(b, row, FieldTaxReferenceNumber, taxReferenceMaxLen, add)
	draft.Jurisdiction = readOptionalText(b, row, FieldJurisdiction, jurisdictionMaxLen, add)
	draft.JurisdictionState = readOptionalText(b, row, FieldJurisdictionState, jurisdictionStateMaxLen, add)
	draft.Currency = readCurrency(b, row, add)
	draft.Periodicity = readRequiredEnum(b, row, TargetObligations, FieldPeriodicity, periodicityVocab, add)

	draft.DeadlineRule = readDeadlineRule(b, row, draft.Periodicity, add)
	return draft, issues
}

// deadlineColumns is what one row said about deadlines, before any decision
// about which of the two strategies it meant.
type deadlineColumns struct {
	declared          string
	periodStart       string
	filingMonths      *int
	filingDays        *int
	paymentMonths     *int
	paymentDays       *int
	fixedDates        []string
	paymentFixedDates []string
	weekendAdjustment string
	// datesUnreadable and offsetsUnreadable mark a deadline cell this reader
	// could not parse. The checks downstream of them are then skipped: telling a
	// customer their row has no filing dates, when the reason is the error
	// already reported two lines up, is noise they have to learn to ignore.
	datesUnreadable   bool
	offsetsUnreadable bool
}

func (c deadlineColumns) hasFixed() bool {
	return len(c.fixedDates) > 0 || len(c.paymentFixedDates) > 0 || c.datesUnreadable
}

func (c deadlineColumns) hasOffset() bool {
	return c.filingMonths != nil || c.filingDays != nil || c.paymentMonths != nil || c.paymentDays != nil
}

func (c deadlineColumns) hasFilingOffset() bool {
	return c.filingMonths != nil || c.filingDays != nil
}

// filledNames is the human name of each deadline column that carried a value,
// for a message that has to say which columns clash.
func (c deadlineColumns) filledNames(b *Binding, fixed bool) []string {
	fields := []string{FieldFilingOffsetMonths, FieldFilingOffsetDays, FieldPaymentOffsetMonths, FieldPaymentOffsetDays}
	filled := []bool{c.filingMonths != nil, c.filingDays != nil, c.paymentMonths != nil, c.paymentDays != nil}
	if fixed {
		fields = []string{FieldFixedDates, FieldPaymentFixedDates}
		filled = []bool{len(c.fixedDates) > 0, len(c.paymentFixedDates) > 0}
	}
	var out []string
	for i, field := range fields {
		if !filled[i] {
			continue
		}
		if header := b.Header(field); header != "" {
			out = append(out, quote(header))
			continue
		}
		out = append(out, quote(field))
	}
	return out
}

// readDeadlineRule assembles the two strategies a flat row can describe.
//
// The API takes a rule that has already chosen between them; a row has to say
// which it means, so a row that fills both without declaring a type is refused
// — that is the one place this import is stricter than the route, and it is
// strict about the file rather than about the tax.
func readDeadlineRule(b *Binding, row Row, periodicity string, add func(Issue)) entityobligationsdomain.DeadlineRule {
	cols := readDeadlineColumns(b, row, add)

	rule := entityobligationsdomain.DeadlineRule{WeekendAdjustment: cols.weekendAdjustment}
	if cols.periodStart != "" {
		if month, day, err := splitMonthDay(cols.periodStart); err == nil {
			rule.PeriodStart = &entityobligationsdomain.PeriodStart{Day: day, Month: month}
		}
	}

	strategy := decideStrategy(b, row, cols, add)
	switch strategy {
	case deadlineTypeFixed:
		rule.Type = deadlineTypeFixed
		rule.FixedDates = cols.fixedDates
		rule.PaymentFixedDates = cols.paymentFixedDates
		checkFixedDates(b, row, cols, periodicity, add)
	case deadlineTypeOffset:
		rule.Type = deadlineTypeOffset
		rule.Reference = referencePeriodEnd
		rule.FilingOffset = &entityobligationsdomain.MonthDayOffset{
			Months: valueOr(cols.filingMonths, 0),
			Days:   valueOr(cols.filingDays, 0),
		}
		if cols.paymentMonths != nil || cols.paymentDays != nil {
			rule.PaymentOffset = &entityobligationsdomain.MonthDayOffset{
				Months: valueOr(cols.paymentMonths, 0),
				Days:   valueOr(cols.paymentDays, 0),
			}
		}
	default:
		// The row describes no complete rule. Whether that is worth a remark —
		// and which remark — depends on the obligation this row will land on,
		// so it is decided by the planner, which is the half that can see one.
	}
	return rule
}

func readDeadlineColumns(b *Binding, row Row, add func(Issue)) deadlineColumns {
	var cols deadlineColumns
	readEnumInto(b, row, FieldDeadlineType, deadlineTypeVocab, &cols.declared, add)
	readEnumInto(b, row, FieldWeekendAdjustment, weekendAdjustmentVocab, &cols.weekendAdjustment, add)

	if value, err := readMonthDay(b, row, FieldPeriodStart); err == nil {
		cols.periodStart = value
	} else if !errors.Is(err, errBlank) {
		add(RowError(b, row, FieldPeriodStart, rawCell(b, row, FieldPeriodStart), err.Error()))
	}

	var offsetFailed [4]bool
	cols.filingMonths, offsetFailed[0] = readOptionalCount(b, row, FieldFilingOffsetMonths, offsetMonthsMax, add)
	cols.filingDays, offsetFailed[1] = readOptionalCount(b, row, FieldFilingOffsetDays, offsetDaysMax, add)
	cols.paymentMonths, offsetFailed[2] = readOptionalCount(b, row, FieldPaymentOffsetMonths, offsetMonthsMax, add)
	cols.paymentDays, offsetFailed[3] = readOptionalCount(b, row, FieldPaymentOffsetDays, offsetDaysMax, add)
	for _, failed := range offsetFailed {
		cols.offsetsUnreadable = cols.offsetsUnreadable || failed
	}
	var filingFailed, paymentFailed bool
	cols.fixedDates, filingFailed = readOptionalMonthDayList(b, row, FieldFixedDates, add)
	cols.paymentFixedDates, paymentFailed = readOptionalMonthDayList(b, row, FieldPaymentFixedDates, add)
	cols.datesUnreadable = filingFailed || paymentFailed

	if n, m := len(cols.fixedDates), len(cols.paymentFixedDates); m > 0 && n != m && !cols.datesUnreadable {
		add(RowError(b, row, FieldPaymentFixedDates, rawCell(b, row, FieldPaymentFixedDates),
			ErrPaymentDatesMismatch(n, m)))
	}
	return cols
}

// decideStrategy settles which of the two deadline strategies the row means,
// and returns "" when it describes neither.
func decideStrategy(b *Binding, row Row, cols deadlineColumns, add func(Issue)) string {
	switch cols.declared {
	case deadlineTypeFixed:
		if !cols.hasFixed() {
			add(RowError(b, row, FieldDeadlineType, cols.declared,
				ErrDeadlineStrategyIncomplete(deadlineTypeFixed, "fixed filing dates")))
			return ""
		}
		if cols.hasOffset() {
			add(RowWarning(b, row, FieldDeadlineType, cols.declared,
				ErrDeadlineStrategyUnused(deadlineTypeFixed, cols.filledNames(b, false))))
		}
		return deadlineTypeFixed
	case deadlineTypeOffset:
		if !cols.hasFilingOffset() {
			add(RowError(b, row, FieldDeadlineType, cols.declared,
				ErrDeadlineStrategyIncomplete(deadlineTypeOffset, "filing offset after period end")))
			return ""
		}
		if cols.hasFixed() {
			add(RowWarning(b, row, FieldDeadlineType, cols.declared,
				ErrDeadlineStrategyUnused(deadlineTypeOffset, cols.filledNames(b, true))))
		}
		return deadlineTypeOffset
	}

	switch {
	case cols.hasFixed() && cols.hasOffset():
		add(RowError(b, row, FieldDeadlineType, "",
			ErrDeadlineStrategyAmbiguous(cols.filledNames(b, true), cols.filledNames(b, false))))
		return ""
	case cols.hasFixed():
		return deadlineTypeFixed
	case cols.hasFilingOffset():
		return deadlineTypeOffset
	case cols.hasOffset():
		// Payment offsets with no filing offset: the filing side is what the
		// strategy is anchored on, so this is incomplete rather than a rule —
		// unless the filing cell is the one that would not read, in which case
		// the error two lines up already says so and this would be the same
		// refusal twice.
		if !cols.offsetsUnreadable {
			add(RowError(b, row, FieldFilingOffsetMonths, "",
				ErrDeadlineStrategyIncomplete(deadlineTypeOffset, "filing offset after period end")))
		}
		return ""
	default:
		return ""
	}
}

// expectedFixedDates is how many calendar dates a periodicity usually carries.
// It is a remark, never a refusal: the route accepts any number.
func expectedFixedDates(periodicity string) (int, bool) {
	switch periodicity {
	case "annual", "consolidated-annual":
		return 1, true
	case "bi-annual":
		return 2, true
	case "quarterly":
		return 4, true
	default:
		return 0, false
	}
}

func checkFixedDates(b *Binding, row Row, cols deadlineColumns, periodicity string, add func(Issue)) {
	want, known := expectedFixedDates(periodicity)
	if !known || cols.datesUnreadable || want == len(cols.fixedDates) {
		return
	}
	add(RowWarning(b, row, FieldFixedDates, strings.Join(cols.fixedDates, ";"),
		ErrFixedDateCount(periodicity, want, len(cols.fixedDates))))
}

// readCurrency reads and upper-cases an ISO 4217 code, then holds it to the
// route's own `len=3,uppercase`.
func readCurrency(b *Binding, row Row, add func(Issue)) *string {
	raw := CleanCell(row.Cell(b.IndexOrMissing(FieldCurrency)))
	if raw == "" {
		return nil
	}
	code := strings.ToUpper(raw)
	if len([]rune(code)) != currencyLen || !isASCIILetters(code) {
		add(RowError(b, row, FieldCurrency, raw, ErrNotACurrency(raw)))
		return nil
	}
	return ptr(code)
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return len(s) > 0
}

// readRequiredEnum is readRequiredText for a vocabulary field, and treats an
// absent column the same way: unspoken, not empty (see readRequiredText).
func readRequiredEnum(b *Binding, row Row, target Target, field string, vocab vocabulary, add func(Issue)) string {
	if !b.Has(field) {
		return ""
	}
	value, err := readEnum(b, row, field, vocab)
	switch {
	case err == nil:
		return value
	case errors.Is(err, errBlank):
		help := ""
		if f, ok := FieldByName(target, field); ok {
			help = f.Help
		}
		add(RowError(b, row, field, "", ErrRequiredValueMissing(labelFor(b, field), help)))
	default:
		add(RowError(b, row, field, rawCell(b, row, field), err.Error()))
	}
	return ""
}

func readOptionalCount(b *Binding, row Row, field string, maxValue int, add func(Issue)) (value *int, failed bool) {
	n, err := readCount(b, row, field, maxValue)
	switch {
	case err == nil:
		return &n, false
	case errors.Is(err, errBlank):
		return nil, false
	default:
		add(RowError(b, row, field, rawCell(b, row, field), err.Error()))
		return nil, true
	}
}

// readOptionalMonthDayList reads a multi-date cell, and reports separately
// whether it FAILED as opposed to being empty — the difference decides whether
// the checks downstream of it are worth running.
func readOptionalMonthDayList(b *Binding, row Row, field string, add func(Issue)) (values []string, failed bool) {
	values, err := readMonthDayList(b, row, field)
	switch {
	case err == nil:
		return values, false
	case errors.Is(err, errBlank):
		return nil, false
	default:
		add(RowError(b, row, field, rawCell(b, row, field), err.Error()))
		return nil, true
	}
}

func rawCell(b *Binding, row Row, field string) string {
	return CleanCell(row.Cell(b.IndexOrMissing(field)))
}

func valueOr(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// splitMonthDay turns a strict MM-DD back into its two numbers, through the
// deadline engine's own parser.
func splitMonthDay(value string) (month, day int, err error) {
	return deadline.ParseMonthDay(value)
}
