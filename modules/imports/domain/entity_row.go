package domain

import (
	"errors"

	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// The bounds are entities/dto.CreateEntityBody's own validate tags, copied so a
// row is measured by exactly what the single-record route measures.
const (
	entityNameMaxLen         = 200
	entityLegalNameMaxLen    = 200
	entityCountryMinLen      = 2
	entityCountryMaxLen      = 100
	entityTaxResidencyMaxLen = 100
	entityParentRefMaxLen    = 200
)

// EntityDraft is one validated entity row: the create contract, ready to be
// handed to the entity use case, plus the parent as the file wrote it.
type EntityDraft struct {
	Name                  string  `json:"name"`
	LegalName             *string `json:"legalName,omitempty"`
	Country               string  `json:"country"`
	TaxResidency          *string `json:"taxResidency,omitempty"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern"`
	FinancialYearEnd      *string `json:"financialYearEnd,omitempty"`
	FiscalWeekEndDay      string  `json:"fiscalWeekEndDay"`
	FiscalYearEndRule     string  `json:"fiscalYearEndRule"`

	// ParentRef is the parent as the file names it: the name of another entity,
	// which may be a row of this same file or an entity already in the tenant.
	// Empty means a top-level entity. Resolving it to an id belongs to the
	// committing half, which is the only half that can see the tenant.
	ParentRef string `json:"parentRef,omitempty"`

	// Spoke names the fields the FILE made a statement about, which is not the
	// same question as whether a cell held a value. A customer who exports name
	// and country from their own system has said nothing about tax residency or
	// the fiscal calendar, so an update must leave those alone; a blank cell in
	// a column they chose to include IS a statement, and clears the value.
	// Without this distinction the two collapse — an absent column reads as an
	// empty one — and a three-column top-up silently proposes clearing
	// everything it does not mention.
	//
	// It is the column set with one refinement: the three fiscal fields that
	// cannot be empty and carry a documented default (pattern, week end day,
	// year end rule) are spoken only when their cell actually says something.
	// A blank cell in one of those columns is not a statement a record can be
	// held to — the value it would clear to does not exist — so it leaves the
	// stored value alone and becomes the default only on a create.
	//
	// It travels with the draft because the dry run and the commit are separate
	// requests: the plan is stored as JSON and the file is gone by then.
	Spoke map[string]bool `json:"spoke,omitempty"`
}

// Said reports whether the file had a column for this field.
func (d EntityDraft) Said(field string) bool { return d.Spoke[field] }

// NaturalKey is what a second import of the same file matches this entity on:
// its name, folded (see NormalizeKey). The file's own parent column already
// refers to entities by name, so the name is the only identifier the file
// actually has.
func (d EntityDraft) NaturalKey() string { return NormalizeKey(d.Name) }

// ParentKey is the natural key of the parent this row names, or "" for a
// top-level entity.
func (d EntityDraft) ParentKey() string { return NormalizeKey(d.ParentRef) }

// CreateInput is the draft as the entity use case takes it. The parent id is
// supplied by the caller, which is the half that resolved ParentRef; nil is a
// top-level entity. CustomPeriods is always absent — a flat file cannot carry
// an ordered list of named periods, and a row that asks for one is refused
// while it is being read.
func (d EntityDraft) CreateInput(parentEntityID *string) entitiesdomain.CreateEntityInput {
	return entitiesdomain.CreateEntityInput{
		ParentEntityID:        parentEntityID,
		Name:                  d.Name,
		LegalName:             d.LegalName,
		Country:               d.Country,
		TaxResidency:          d.TaxResidency,
		FiscalCalendarPattern: d.FiscalCalendarPattern,
		FinancialYearEnd:      d.FinancialYearEnd,
		FiscalWeekEndDay:      d.FiscalWeekEndDay,
		FiscalYearEndRule:     d.FiscalYearEndRule,
	}
}

// readEntityRow reads and validates one entity row, reporting every problem it
// finds rather than stopping at the first: a customer fixing a file needs the
// whole list, not one line of it per upload.
func readEntityRow(b *Binding, row Row) (*EntityDraft, []Issue) {
	var issues []Issue
	add := func(i Issue) { issues = append(issues, i) }

	draft := &EntityDraft{
		FiscalCalendarPattern: entitiesdomain.DefaultFiscalCalendarPattern,
		FiscalWeekEndDay:      entitiesdomain.DefaultFiscalWeekEndDay,
		FiscalYearEndRule:     entitiesdomain.DefaultFiscalYearEndRule,
		Spoke:                 spokenFields(b, TargetEntities),
	}

	draft.Name = readRequiredText(b, row, TargetEntities, FieldName, 1, entityNameMaxLen, add)
	draft.Country = readRequiredText(b, row, TargetEntities, FieldCountry, entityCountryMinLen, entityCountryMaxLen, add)
	draft.LegalName = readOptionalText(b, row, FieldLegalName, entityLegalNameMaxLen, add)
	draft.TaxResidency = readOptionalText(b, row, FieldTaxResidency, entityTaxResidencyMaxLen, add)

	if parent := readOptionalText(b, row, FieldParent, entityParentRefMaxLen, add); parent != nil {
		draft.ParentRef = *parent
		if draft.Name != "" && NormalizeKey(draft.ParentRef) == NormalizeKey(draft.Name) {
			add(RowError(b, row, FieldParent, draft.ParentRef, ErrSelfParent(draft.Name)))
			draft.ParentRef = ""
		}
	}

	readDefaultedEnum(b, row, FieldFiscalCalendarPattern, fiscalPatternVocab, draft, &draft.FiscalCalendarPattern, add)
	readDefaultedEnum(b, row, FieldFiscalWeekEndDay, weekEndDayVocab, draft, &draft.FiscalWeekEndDay, add)
	readDefaultedEnum(b, row, FieldFiscalYearEndRule, yearEndRuleVocab, draft, &draft.FiscalYearEndRule, add)

	if draft.FiscalCalendarPattern == deadline.PatternCustom {
		add(RowError(b, row, FieldFiscalCalendarPattern,
			CleanCell(row.Cell(b.IndexOrMissing(FieldFiscalCalendarPattern))),
			ErrCustomCalendarNotImportable()))
		draft.FiscalCalendarPattern = entitiesdomain.DefaultFiscalCalendarPattern
	}

	if value, err := readMonthDay(b, row, FieldFinancialYearEnd); err == nil {
		draft.FinancialYearEnd = ptr(value)
	} else if !errors.Is(err, errBlank) {
		add(RowError(b, row, FieldFinancialYearEnd,
			CleanCell(row.Cell(b.IndexOrMissing(FieldFinancialYearEnd))), err.Error()))
	}

	// The last word on the fiscal calendar is the entity module's own — the
	// exact function modules/entities/usecases.Create calls before it writes.
	if msg := entitiesdomain.ValidateFiscalConfig(entitiesdomain.FiscalConfig{
		FiscalCalendarPattern: draft.FiscalCalendarPattern,
		FinancialYearEnd:      draft.FinancialYearEnd,
		FiscalWeekEndDay:      draft.FiscalWeekEndDay,
		FiscalYearEndRule:     draft.FiscalYearEndRule,
	}); msg != "" {
		add(RowError(b, row, FieldFiscalCalendarPattern, draft.FiscalCalendarPattern, ErrFiscalConfig(msg)))
	}

	return draft, issues
}

// readRequiredText reads a field the route will not accept empty.
//
// An absent COLUMN is not an empty cell, and is not this half's to refuse: the
// customer said nothing about that field, which leaves a stored value standing
// and refuses only a row that creates — a verdict only the planner can reach
// (MissingOnCreate). Naming a cell that does not exist would be the wrong
// message in any case. A column that IS there and blank is a statement, and
// this is where it is refused.
func readRequiredText(b *Binding, row Row, target Target, field string, minLen, maxLen int, add func(Issue)) string {
	if !b.Has(field) {
		return ""
	}
	value, err := readText(b, row, field, minLen, maxLen)
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
		add(RowError(b, row, field, CleanCell(row.Cell(b.IndexOrMissing(field))), err.Error()))
	}
	return ""
}

// readOptionalText reads a field that may be absent, returning nil when it is.
func readOptionalText(b *Binding, row Row, field string, maxLen int, add func(Issue)) *string {
	value, err := readText(b, row, field, 0, maxLen)
	switch {
	case err == nil:
		return ptr(value)
	case errors.Is(err, errBlank):
		return nil
	default:
		add(RowError(b, row, field, CleanCell(row.Cell(b.IndexOrMissing(field))), err.Error()))
		return nil
	}
}

// readEnumInto overwrites a default only when the cell actually says something,
// and reports whether it did.
func readEnumInto(b *Binding, row Row, field string, vocab vocabulary, into *string, add func(Issue)) bool {
	value, err := readEnum(b, row, field, vocab)
	switch {
	case err == nil:
		*into = value
		return true
	case errors.Is(err, errBlank):
		return false
	default:
		add(RowError(b, row, field, CleanCell(row.Cell(b.IndexOrMissing(field))), err.Error()))
		return false
	}
}

// readDefaultedEnum reads one of the fiscal enums that cannot be empty and has
// a documented default, and unspeaks the field when the cell is blank — see
// EntityDraft.Spoke. A column of blanks then leaves a 4-4-5 register alone
// instead of resetting every entity of it to the standard calendar.
func readDefaultedEnum(
	b *Binding, row Row, field string, vocab vocabulary, draft *EntityDraft, into *string, add func(Issue),
) {
	if !readEnumInto(b, row, field, vocab, into, add) {
		delete(draft.Spoke, field)
	}
}
