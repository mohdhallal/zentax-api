package domain

import (
	"strings"
	"unicode"
)

// Target names what a file is being imported as. Workflows and task instances
// are deliberately absent: this wave imports the standing tax book, not the
// work scheduled against it.
type Target string

const (
	TargetEntities    Target = "entities"
	TargetObligations Target = "entity-obligations"
)

// Label is the target's name in a sentence.
func (t Target) Label() string {
	if t == TargetObligations {
		return "entity obligations"
	}
	return "entities"
}

// LabelSingular is one record of the target, in a sentence.
func (t Target) LabelSingular() string {
	if t == TargetObligations {
		return "entity obligation"
	}
	return "entity"
}

// Valid reports whether the target is one this package can import.
func (t Target) Valid() bool {
	return t == TargetEntities || t == TargetObligations
}

// Field names. Where a field exists on the single-record route it is spelled
// exactly as that route's JSON field, so a column and a request body are
// demonstrably talking about the same thing. The four that do not exist there —
// FieldParent, FieldObligationType and the flattened deadline offsets — are the
// price of a flat file: a JSON body nests, a spreadsheet row cannot.
const (
	FieldName                  = "name"
	FieldLegalName             = "legalName"
	FieldCountry               = "country"
	FieldTaxResidency          = "taxResidency"
	FieldParent                = "parent"
	FieldFiscalCalendarPattern = "fiscalCalendarPattern"
	FieldFinancialYearEnd      = "financialYearEnd"
	FieldFiscalWeekEndDay      = "fiscalWeekEndDay"
	FieldFiscalYearEndRule     = "fiscalYearEndRule"

	FieldEntity              = "entity"
	FieldObligationType      = "obligationType"
	FieldTaxReferenceNumber  = "taxReferenceNumber"
	FieldJurisdiction        = "jurisdiction"
	FieldJurisdictionState   = "jurisdictionState"
	FieldCurrency            = "currency"
	FieldPeriodicity         = "periodicity"
	FieldDeadlineType        = "deadlineType"
	FieldPeriodStart         = "periodStart"
	FieldFilingOffsetMonths  = "filingOffsetMonths"
	FieldFilingOffsetDays    = "filingOffsetDays"
	FieldPaymentOffsetMonths = "paymentOffsetMonths"
	FieldPaymentOffsetDays   = "paymentOffsetDays"
	FieldFixedDates          = "fixedDates"
	FieldPaymentFixedDates   = "paymentFixedDates"
	FieldWeekendAdjustment   = "weekendAdjustment"
)

// Field is one importable column.
//
// Aliases are the header spellings accepted for the field, the canonical one
// first. Matching is EXACT on the normalised form (NormalizeHeader) and never
// fuzzy: an edit-distance match binds the wrong column silently, and a column
// bound to the wrong field is the one import failure a customer cannot see in
// the report — every other mistake shows up as a value they can read. A header
// this list does not know is reported as not imported, never guessed at.
// Required means required to WRITE a record, which is not the same as required
// in every file. A file that carries the key and the one column it is
// correcting is exactly the file a customer should be able to send — the
// stored record answers for everything the file does not mention (see
// EntityDraft.Spoke) — so a required column that is absent refuses only the
// rows that CREATE, per row, where the planner can see which those are.
//
// Key marks the columns that identify the record: without them a row cannot be
// matched to anything and there is nothing to read from, so those are still
// required of the file itself, and their absence refuses it whole.
type Field struct {
	Name     string   `json:"name"`
	Required bool     `json:"required"`
	Key      bool     `json:"-"`
	Aliases  []string `json:"aliases"`
	Help     string   `json:"help"`
}

// entityFields is the importable column set for TargetEntities: exactly the
// create contract (modules/entities/dto.CreateEntityBody), minus customPeriods
// — an ordered list of named periods is not a thing a flat row can carry — and
// with parentEntityId replaced by a parent reference a spreadsheet can write.
var entityFields = []Field{
	{
		Name: FieldName, Required: true, Key: true,
		Aliases: []string{"name", "entity name", "entity", "company", "company name", "subsidiary", "legal entity"},
		Help:    "the entity's name in ZenTax, and what other rows' parent column refers to",
	},
	{
		Name:    FieldLegalName,
		Aliases: []string{"legal name", "registered name", "statutory name", "full legal name"},
		Help:    "the registered name, when it differs from the name you use day to day",
	},
	{
		Name: FieldCountry, Required: true,
		Aliases: []string{"country", "country of incorporation", "incorporation country", "domicile"},
		Help:    "the country the entity is incorporated in",
	},
	{
		Name:    FieldTaxResidency,
		Aliases: []string{"tax residency", "tax residence", "tax resident", "residency"},
		Help:    "the country the entity is tax resident in, when it differs from the country",
	},
	{
		Name:    FieldParent,
		Aliases: []string{"parent", "parent entity", "parent company", "parent name", "parent entity name", "subsidiary of", "owned by"},
		Help:    "the NAME of the parent entity (a row of this same file, or an entity already in ZenTax); leave blank for a top-level entity",
	},
	{
		Name:    FieldFiscalCalendarPattern,
		Aliases: []string{"fiscal calendar pattern", "fiscal calendar", "calendar pattern", "fiscal pattern", "period pattern"},
		Help:    "standard, 445, 454, 544, 13-period or weekly (blank means standard)",
	},
	{
		Name:    FieldFinancialYearEnd,
		Aliases: []string{"financial year end", "fiscal year end", "year end", "fye", "fy end"},
		Help:    "the month and day the financial year ends, as MM-DD — 12-31 for a calendar year",
	},
	{
		Name:    FieldFiscalWeekEndDay,
		Aliases: []string{"fiscal week end day", "week end day", "week ends on", "fiscal week ending"},
		Help:    "which weekday a fiscal week ends on, for the week-based patterns (blank means saturday)",
	},
	{
		Name:    FieldFiscalYearEndRule,
		Aliases: []string{"fiscal year end rule", "year end rule", "fy end rule"},
		Help:    "last or nearest — whether the fiscal year ends on the last such weekday on or before the year end, or the nearest one (blank means nearest)",
	},
}

// obligationFields is the importable column set for TargetObligations: the
// create contract (modules/entityobligations/dto.CreateEntityObligationBody)
// with the two UUID references written as names and the deadline rule flattened
// into the columns a spreadsheet can hold.
var obligationFields = []Field{
	{
		Name: FieldEntity, Required: true, Key: true,
		Aliases: []string{"entity", "entity name", "company", "company name", "legal entity"},
		Help:    "the NAME of the entity this obligation belongs to, as the entity import spells it",
	},
	{
		Name: FieldObligationType, Required: true, Key: true,
		Aliases: []string{"obligation type", "obligation", "obligation code", "obligation type code", "obligation type name", "tax type", "tax", "type"},
		Help:    "the obligation type's code or name as it exists in ZenTax (VAT-RET, CIT-ANN, …)",
	},
	{
		Name:    FieldTaxReferenceNumber,
		Aliases: []string{"tax reference number", "tax reference", "tax registration number", "registration number", "tax id", "tax number", "vat number", "trn"},
		Help:    "the reference the tax authority issued for this registration",
	},
	{
		Name:    FieldJurisdiction,
		Aliases: []string{"jurisdiction", "jurisdiction country", "filing country", "country"},
		Help:    "the country the obligation is filed in",
	},
	{
		Name:    FieldJurisdictionState,
		Aliases: []string{"jurisdiction state", "state", "province", "state province", "region", "canton"},
		Help:    "the state or province, for a sub-national tax",
	},
	{
		Name:    FieldCurrency,
		Aliases: []string{"currency", "reporting currency", "filing currency", "ccy"},
		Help:    "the obligation's currency as an ISO 4217 code (EUR, USD, GBP)",
	},
	{
		Name: FieldPeriodicity, Required: true,
		Aliases: []string{"periodicity", "frequency", "filing frequency", "filing periodicity", "period"},
		Help:    "monthly, quarterly, bi-annual, annual, consolidated-annual or weekly",
	},
	{
		Name:    FieldDeadlineType,
		Aliases: []string{"deadline type", "deadline rule", "deadline rule type", "rule type"},
		Help:    "fixed (calendar dates every year) or period_offset (a delay after each period ends); blank is read from whichever columns you filled",
	},
	{
		Name:    FieldPeriodStart,
		Aliases: []string{"period start", "reporting period start", "period start date", "cycle start"},
		Help:    "the month and day the reporting cycle starts, as MM-DD",
	},
	{
		Name:    FieldFilingOffsetMonths,
		Aliases: []string{"filing offset months", "filing months after period end", "filing due months", "filing deadline months"},
		Help:    "whole months after the period ends until the return is due",
	},
	{
		Name:    FieldFilingOffsetDays,
		Aliases: []string{"filing offset days", "filing days after period end", "filing due days", "filing deadline days"},
		Help:    "days after the period ends (on top of the months) until the return is due",
	},
	{
		Name:    FieldPaymentOffsetMonths,
		Aliases: []string{"payment offset months", "payment months after period end", "payment due months", "payment deadline months"},
		Help:    "whole months after the period ends until payment is due; leave both payment columns blank when payment is due with the filing",
	},
	{
		Name:    FieldPaymentOffsetDays,
		Aliases: []string{"payment offset days", "payment days after period end", "payment due days", "payment deadline days"},
		Help:    "days after the period ends (on top of the months) until payment is due",
	},
	{
		Name:    FieldFixedDates,
		Aliases: []string{"fixed dates", "filing dates", "fixed filing dates", "due dates", "filing due dates", "deadline dates"},
		Help:    "the calendar filing dates as MM-DD, separated by semicolons — 07-31;01-31",
	},
	{
		Name: FieldPaymentFixedDates,
		// "payment fixed dates" folds to the field's own name, which the
		// generated template writes as its header: without it the sheet the
		// product hands out carries a column the product then ignores.
		Aliases: []string{"payment fixed dates", "payment dates", "fixed payment dates", "payment due dates", "fixed payment date"},
		Help:    "the calendar payment dates as MM-DD, one for each filing date, in the same order",
	},
	{
		Name:    FieldWeekendAdjustment,
		Aliases: []string{"weekend adjustment", "weekend rule", "if weekend", "weekend handling"},
		Help:    "none, next-business-day or prev-business-day — what happens when a deadline lands on a weekend",
	},
}

// Fields is the importable column set of a target, in the order a template
// would write them.
func Fields(target Target) []Field {
	switch target {
	case TargetObligations:
		return obligationFields
	case TargetEntities:
		return entityFields
	default:
		return nil
	}
}

// sheetNameAliases are the tab names a workbook uses for each target.
var sheetNameAliases = map[Target][]string{
	TargetEntities:    {"entities", "entity", "companies", "company", "legal entities", "subsidiaries", "group structure", "structure"},
	TargetObligations: {"entity obligations", "obligations", "obligation", "tax obligations", "registrations", "registration", "taxes", "filings", "obligation register"},
}

func matchesSheetName(name string, target Target) bool {
	normalized := NormalizeHeader(name)
	if normalized == "" {
		return false
	}
	for _, alias := range sheetNameAliases[target] {
		if NormalizeHeader(alias) == normalized {
			return true
		}
	}
	return false
}

// headerIndex is the normalised alias → field name lookup per target, built
// once. A duplicate alias between two fields of the same target would make
// binding unpredictable, so building panics on one: it is a programming error
// in this file, not a runtime condition.
var headerIndex = map[Target]map[string]string{
	TargetEntities:    buildHeaderIndex(entityFields),
	TargetObligations: buildHeaderIndex(obligationFields),
}

func buildHeaderIndex(fields []Field) map[string]string {
	index := make(map[string]string, len(fields)*4)
	for _, f := range fields {
		for _, alias := range f.Aliases {
			normalized := NormalizeHeader(alias)
			if existing, clash := index[normalized]; clash {
				panic("imports: header alias " + alias + " is claimed by both " + existing + " and " + f.Name)
			}
			index[normalized] = f.Name
		}
	}
	return index
}

// FieldForHeader maps a header cell as the customer wrote it to the field it
// binds, if any.
func FieldForHeader(target Target, header string) (string, bool) {
	normalized := NormalizeHeader(header)
	if normalized == "" {
		return "", false
	}
	name, ok := headerIndex[target][normalized]
	return name, ok
}

// FieldByName returns a target's field definition.
func FieldByName(target Target, name string) (Field, bool) {
	for _, f := range Fields(target) {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// NormalizeHeader folds a header cell to the form aliases are matched on: any
// parenthesised or bracketed hint is dropped whole ("Financial year end
// (MM-DD)" is the same column as "Financial year end"), then everything that is
// not a letter or a digit goes and what is left is lower-cased. So "Entity
// Name", "entity_name", "ENTITY NAME *" and "Entity  name" are one header, and
// nothing about spacing, punctuation or a required-field asterisk can cost a
// customer a column.
func NormalizeHeader(header string) string {
	var sb strings.Builder
	sb.Grow(len(header))
	depth := 0
	for _, r := range header {
		switch {
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			if depth > 0 {
				depth--
			}
		case depth > 0:
			// Inside a hint — ignored.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}
