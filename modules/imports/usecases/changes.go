package usecases

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// An import UPDATE merges. It does not replace.
//
// A column a file does not have is a column the customer said nothing about, so
// it is neither compared, nor listed as a change, nor written: export name and
// country out of your own system, import that over a register whose entities
// have legal names, parents and March year ends, and every one of those is left
// exactly as it is. A column they DID include and left blank is a statement —
// "this field is empty" — and clears the value.
//
// The drafts carry the difference (EntityDraft.Spoke / ObligationDraft.Spoke).
// This file is where it is acted on, and it is acted on in TWO places that must
// agree: what is compared, which is the promise the dry run makes, and what is
// written, which is what the commit does. Both read the same Said(), so the
// promise and the effect cannot drift.
//
// The merge is expressed as "write the stored value back" rather than as a
// narrower UPDATE statement, and that is safe for one specific reason: the
// stored value comes from the read the COMMIT made, inside its own transaction,
// and the statement is pinned to the version that read returned. A record
// someone edited in between fails the version check and refuses the whole
// batch; it is never quietly rewritten with values from a stale read.

const (
	deadlineTypeFixed  = "fixed"
	deadlineTypeOffset = "period_offset"
)

// changeSet collects the fields an update moves, with both values, skipping
// every field the file said nothing about.
type changeSet struct {
	said    func(string) bool
	changes []domain.FieldChange
}

// compare records a field the file spoke about whose value moves.
//
// "Moves" is judged with the several Unicode spellings of one string folded
// together (domain.SameText), because they are one value: a name typed into the
// browser is precomposed, the same name in a list edited on macOS is not, and
// an import that reported `name: Müller GmbH → Müller GmbH` would be showing a
// customer a change whose two sides are identical on screen — and then writing
// it. Case and spacing still count as changes; correcting either is a real edit.
func (c *changeSet) compare(field string, from, to *string) {
	if !c.said(field) || sameText(from, to) {
		return
	}
	c.record(field, from, to)
}

// sameText is samePtr over values rather than over bytes.
func sameText(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return domain.SameText(*a, *b)
	}
}

// record adds a change the caller has already decided on: the two fields whose
// difference is not a text comparison — the parent, compared by id, and the
// deadline rule, compared by value.
func (c *changeSet) record(field string, from, to *string) {
	c.changes = append(c.changes, domain.FieldChange{Field: field, From: from, To: to})
}

// sorted returns the changes in field order, so two dry runs of one file
// produce the same report.
func (c *changeSet) sorted() []domain.FieldChange {
	sort.Slice(c.changes, func(i, j int) bool { return c.changes[i].Field < c.changes[j].Field })
	return c.changes
}

// ── what a commit writes ────────────────────────────────────────────────────

// mergeEntity is the draft as it will be WRITTEN over an existing record: the
// file's own value for every field it spoke about, the record's current value
// for every field it did not.
func mergeEntity(draft *domain.EntityDraft, target domain.ExistingEntity) *domain.EntityDraft {
	merged := *draft
	if !draft.Said(domain.FieldName) {
		merged.Name = target.Name
	}
	if !draft.Said(domain.FieldLegalName) {
		merged.LegalName = target.LegalName
	}
	if !draft.Said(domain.FieldCountry) {
		merged.Country = target.Country
	}
	if !draft.Said(domain.FieldTaxResidency) {
		merged.TaxResidency = target.TaxResidency
	}
	if !draft.Said(domain.FieldFiscalCalendarPattern) {
		merged.FiscalCalendarPattern = target.Pattern
	}
	if !draft.Said(domain.FieldFinancialYearEnd) {
		merged.FinancialYearEnd = target.YearEnd
	}
	if !draft.Said(domain.FieldFiscalWeekEndDay) {
		merged.FiscalWeekEndDay = target.WeekEndDay
	}
	if !draft.Said(domain.FieldFiscalYearEndRule) {
		merged.FiscalYearEndRule = target.YearEndRule
	}
	return &merged
}

// mergeParent is the parent id an update writes: the one the file names, or the
// one the record already has when the file has no parent column. A file with no
// parent column never detaches an entity from its group.
func mergeParent(draft *domain.EntityDraft, parentID *string, target domain.ExistingEntity) *string {
	if !draft.Said(domain.FieldParent) {
		return target.ParentID
	}
	return parentID
}

// mergeObligation is the obligation draft as it will be written, on the same
// rule — with the deadline rule treated as ONE field rather than as the dozen
// columns it is flattened into, because half a rule is not a rule: a row that
// does not describe a complete one leaves the stored rule where it is.
func mergeObligation(
	draft *domain.ObligationDraft, target domain.ExistingObligation,
) (*domain.ObligationDraft, error) {
	merged := *draft
	if !draft.Said(domain.FieldTaxReferenceNumber) {
		merged.TaxReferenceNumber = target.TaxReferenceNumber
	}
	if !draft.Said(domain.FieldJurisdiction) {
		merged.Jurisdiction = target.Jurisdiction
	}
	if !draft.Said(domain.FieldJurisdictionState) {
		merged.JurisdictionState = target.JurisdictionState
	}
	if !draft.Said(domain.FieldCurrency) {
		merged.Currency = target.Currency
	}
	if !draft.Said(domain.FieldPeriodicity) {
		merged.Periodicity = target.Periodicity
	}
	stored, err := storedRule(target)
	if err != nil {
		return nil, err
	}
	merged.DeadlineRule = mergedRule(draft, stored)
	return &merged, nil
}

// mergedRule is the deadline rule an update would write.
//
// A row that does not describe a complete rule leaves the stored one exactly as
// it is. A row that DOES describe one replaces the strategy and its dates
// together — filing dates and payment dates are index-paired, and offsets are
// read as a set, so merging half of one file's rule into half of a stored rule
// would make a schedule neither of them describes. What it does not replace is
// what no column of that file touched: the weekend rule, the cycle start, and
// the extra per-period deadlines, which have no column at all because a flat
// row cannot carry a list of them.
func mergedRule(
	draft *domain.ObligationDraft, stored entityobligationsdomain.DeadlineRule,
) entityobligationsdomain.DeadlineRule {
	if !describesRule(draft) {
		return stored
	}
	merged := draft.DeadlineRule
	if !draft.Said(domain.FieldWeekendAdjustment) {
		merged.WeekendAdjustment = stored.WeekendAdjustment
	}
	if !draft.Said(domain.FieldPeriodStart) {
		merged.PeriodStart = stored.PeriodStart
	}
	merged.AdditionalDeadlines = stored.AdditionalDeadlines
	return merged
}

// decodeRule is storedRule for the PLANNING half, which describes a rule it
// cannot decode rather than refusing: the commit refuses it, and a dry run that
// errored would leave the customer without the report that explains why.
func decodeRule(raw json.RawMessage) entityobligationsdomain.DeadlineRule {
	var rule entityobligationsdomain.DeadlineRule
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rule)
	}
	return rule
}

// describesRule reports whether the row describes a COMPLETE deadline rule: a
// strategy together with the values it is made of. Anything less — a weekend
// adjustment on its own, a period start, no deadline columns at all — is not a
// statement about how a filing date is computed, and an import never erases a
// filing calendar on the strength of columns that are not there.
func describesRule(draft *domain.ObligationDraft) bool { return draft.DeadlineRule.Type != "" }

// partialRule reports whether a row filled a deadline cell without describing a
// rule. Those cells are not applied, which is a thing the customer is told
// rather than left to infer from a report that shows no change.
func partialRule(draft *domain.ObligationDraft) bool {
	return !describesRule(draft) &&
		(draft.DeadlineRule.PeriodStart != nil || draft.DeadlineRule.WeekendAdjustment != "")
}

// storedRule decodes the rule an obligation carries today. A rule that cannot
// be read back refuses the commit rather than being replaced by an empty one:
// it is what dates a statutory filing, and nothing versions it.
func storedRule(target domain.ExistingObligation) (entityobligationsdomain.DeadlineRule, error) {
	var rule entityobligationsdomain.DeadlineRule
	if len(target.DeadlineRule) == 0 {
		return rule, nil
	}
	if err := json.Unmarshal(target.DeadlineRule, &rule); err != nil {
		return rule, apperrors.NewConflict(
			"the deadline rule stored on one of the obligations this file updates could not be read," +
				" so this import will not rewrite it. Nothing was imported")
	}
	return rule, nil
}

// ── what a change list shows ────────────────────────────────────────────────

// storedSchedules reports whether the obligation's stored rule actually
// computes a deadline — a strategy, not a fragment of one. A rule this code
// cannot decode counts as one: the honest assumption about something
// unreadable is that it matters.
func storedSchedules(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var rule entityobligationsdomain.DeadlineRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return true
	}
	return rule.Type != ""
}

// describeStoredRule renders the rule an obligation carries today. A rule this
// code cannot decode is shown as it is stored rather than as nothing: "(empty)"
// against a rule that exists would be the one lie a preview must not tell.
func describeStoredRule(raw json.RawMessage) *string {
	if len(raw) == 0 {
		return nil
	}
	var rule entityobligationsdomain.DeadlineRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return ptrOf(string(raw))
	}
	return describeRule(rule)
}

// describeRule is a deadline rule as a sentence — "1 month 7 days after period
// end, weekends move to the next business day" — because a change list is read
// by a tax manager, and the JSON that computes their filing dates is not a
// thing they can check. nil is a rule that says nothing.
func describeRule(rule entityobligationsdomain.DeadlineRule) *string {
	var parts []string
	switch rule.Type {
	case deadlineTypeFixed:
		if len(rule.FixedDates) > 0 {
			parts = append(parts, "fixed filing dates "+strings.Join(rule.FixedDates, ", "))
		}
		if len(rule.PaymentFixedDates) > 0 {
			parts = append(parts, "payment dates "+strings.Join(rule.PaymentFixedDates, ", "))
		}
	case deadlineTypeOffset:
		switch {
		case rule.FilingOffset != nil:
			parts = append(parts, describeOffset(*rule.FilingOffset)+" after "+describeReference(rule.Reference))
		case rule.OffsetUnit != "":
			// The older offset shape, which a record written before the
			// month-and-day pair can still be carrying.
			parts = append(parts, fmt.Sprintf("%d %s %s %s",
				rule.OffsetValue, rule.OffsetUnit, orElse(rule.OffsetDirection, "after"),
				describeReference(rule.Reference)))
		}
		if rule.PaymentOffset != nil {
			parts = append(parts, "payment "+describeOffset(*rule.PaymentOffset)+
				" after "+describeReference(rule.Reference))
		}
	}
	if rule.PeriodStart != nil {
		parts = append(parts, fmt.Sprintf("cycle starts %02d-%02d", rule.PeriodStart.Month, rule.PeriodStart.Day))
	}
	if rule.WeekendAdjustment != "" && rule.WeekendAdjustment != "none" {
		parts = append(parts, "weekends move to the "+strings.ReplaceAll(rule.WeekendAdjustment, "-", " "))
	}
	if n := len(rule.AdditionalDeadlines); n > 0 {
		parts = append(parts, plural(n, "extra deadline", "extra deadlines"))
	}
	if len(parts) == 0 {
		return nil
	}
	return ptrOf(strings.Join(parts, ", "))
}

func describeOffset(o entityobligationsdomain.MonthDayOffset) string {
	switch {
	case o.Months > 0 && o.Days > 0:
		return plural(o.Months, "month", "months") + " " + plural(o.Days, "day", "days")
	case o.Months > 0:
		return plural(o.Months, "month", "months")
	default:
		return plural(o.Days, "day", "days")
	}
}

func describeReference(reference string) string {
	if reference == "filing_deadline" {
		return "the filing deadline"
	}
	return "period end"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func orElse(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ptrOf is a pointer to a copy of a value, for the change list's optional
// strings.
func ptrOf(s string) *string { return &s }

// textOrNil renders an empty string as "no value", which is what a change list
// has to distinguish from the empty string itself.
func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
