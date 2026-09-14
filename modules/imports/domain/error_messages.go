package domain

import (
	"strconv"
	"strings"
)

// Messages are written for the person who has to fix the file — a tax manager
// looking at their own spreadsheet, not at ZenTax's schema. Each one says what
// was read, why it was refused, and what to write instead.

// ErrNoHeaderRow is the refusal of a file whose columns cannot be recognised at
// all: either it is not the sheet the customer meant to send, or its headers
// are spelled in a way this import does not know.
func ErrNoHeaderRow(target Target) string {
	var required []string
	for _, f := range Fields(target) {
		if f.Required {
			required = append(required, quoteList(f.Aliases[:1]))
		}
	}
	return "no header row found in the first " + strconv.Itoa(MaxHeaderScanRows) +
		" rows: this file does not name any two columns of a ZenTax " + target.Label() +
		" import. It needs at least " + joinAnd(required) +
		" as column headings — a title or a blank line above them is fine."
}

// ErrRequiredColumnMissing names a column the import cannot do without, in
// every spelling it would have accepted.
func ErrRequiredColumnMissing(f Field) string {
	msg := "the " + quote(f.Aliases[0]) + " column is missing — it holds " + f.Help + "."
	if len(f.Aliases) > 1 {
		msg += " These headings are also read as " + quote(f.Aliases[0]) + ": " + quoteList(f.Aliases[1:]) + "."
	}
	return msg
}

// ErrRequiredColumnMissingForCreate refuses a ROW that would create a record
// while the file has no column for a field a new record needs. It says which
// rows are affected and which are not, because the same file is a perfectly
// good correction sheet for everything already in ZenTax.
func ErrRequiredColumnMissingForCreate(target Target, f Field) string {
	return "this row would create a new " + target.LabelSingular() + ", and this file has no " +
		quote(f.Aliases[0]) + " column — a new " + target.LabelSingular() + " needs one: it holds " + f.Help +
		". Add the column for the rows that create, or import those rows on their own;" +
		" rows that match a record already in ZenTax are not affected."
}

// ErrColumnClaimedTwice refuses a file where one field has two columns. Picking
// one would be a guess, and a guess here silently imports the wrong column.
func ErrColumnClaimedTwice(field, firstCol, firstHeader, secondCol, secondHeader string) string {
	return "columns " + firstCol + " (" + quote(firstHeader) + ") and " + secondCol + " (" +
		quote(secondHeader) + ") are both read as " + quote(field) +
		" — rename or remove one of them so there is only one."
}

// ErrColumnsNotImported names every column that will not arrive. It is a
// warning, not a refusal: an export always carries columns ZenTax has no home
// for, and refusing the file for that would make real exports unimportable.
func ErrColumnsNotImported(target Target, ignored []IgnoredColumn) string {
	named := make([]string, 0, len(ignored))
	for _, c := range ignored {
		if c.Header == NoHeading {
			// Not quoted: the customer wrote nothing there, and quoting our own
			// words back at them would read as a heading they cannot find.
			named = append(named, c.Column+" "+NoHeading)
			continue
		}
		named = append(named, c.Column+" ("+quote(c.Header)+")")
	}
	msg := strconv.Itoa(len(ignored)) + " " + pluralize(len(ignored), "column is", "columns are") +
		" not part of a ZenTax " +
		target.Label() + " import and will not be imported: " + strings.Join(named, ", ") + "."
	if target == TargetEntities {
		msg += " Workflows and task instances are not imported in any case — they are generated" +
			" from an entity's obligations once those are in."
	}
	return msg
}

// ErrRequiredValueMissing is an empty cell in a column that must carry a value.
func ErrRequiredValueMissing(label, help string) string {
	return "the " + quote(label) + " cell is empty on this row, and it cannot be — it holds " + help + "."
}

// ErrLength is the single-record route's own min/max on a text field.
func ErrLength(label string, minLen, maxLen int) string {
	switch {
	case maxLen <= 0:
		return quote(label) + " must be at least " + strconv.Itoa(minLen) + " characters."
	case minLen <= 1:
		return quote(label) + " must be at most " + strconv.Itoa(maxLen) + " characters."
	default:
		return quote(label) + " must be between " + strconv.Itoa(minLen) + " and " +
			strconv.Itoa(maxLen) + " characters."
	}
}

// ErrNotInVocabulary refuses a value outside one of the API's enums, and always
// prints the whole enum: fixing a file should never need a guess.
func ErrNotInVocabulary(label, value string, allowed []string) string {
	return quote(value) + " is not a " + strings.ToLower(label) + " ZenTax knows — use one of: " +
		strings.Join(allowed, ", ") + "."
}

// ErrNotAWholeNumber refuses a count that is not a whole number.
func ErrNotAWholeNumber(label, unit, value string) string {
	return quote(label) + " must be a whole number of " + unit + ", and " + quote(value) + " is not."
}

// ErrOutOfRange refuses a count outside the bound the deadline rule carries.
func ErrOutOfRange(label, unit, value string, minValue, maxValue int) string {
	return quote(label) + " must be between " + strconv.Itoa(minValue) + " and " +
		strconv.Itoa(maxValue) + " " + unit + ", and " + quote(value) + " is not."
}

// ErrNotAMonthDay refuses a date, quoting the reason the reader gave — which
// for an ambiguous value spells out both readings.
func ErrNotAMonthDay(label, value string, cause error) string {
	return quote(value) + " in " + quote(label) + " is not a month and day: " + cause.Error() +
		". Write it as MM-DD — 12-31 for 31 December."
}

// ErrCustomCalendarNotImportable is the one fiscal pattern a flat file cannot
// express: a custom calendar is an ordered list of named periods, and
// entities/domain.ValidateFiscalConfig requires at least one of them.
func ErrCustomCalendarNotImportable() string {
	return "a custom fiscal calendar cannot be described in a spreadsheet row: it is a list of named" +
		" periods. Import the entity on another pattern and set its custom periods afterwards."
}

// ErrFiscalConfig carries a refusal from entities/domain.ValidateFiscalConfig
// through unchanged — the same words the single-record route would have used.
func ErrFiscalConfig(message string) string {
	return message + " (the same rule the entity form applies)."
}

// ErrSelfParent refuses a row that makes an entity its own parent.
func ErrSelfParent(name string) string {
	return quote(name) + " is named as its own parent — leave the parent column empty for a" +
		" top-level entity, or name the entity above it."
}

// ErrDuplicateEntity refuses the second of two rows describing one entity. The
// file's own parent column refers to entities by name, so a repeated name has
// no single meaning even before anything is written.
func ErrDuplicateEntity(firstName string, firstRow int) string {
	return "row " + strconv.Itoa(firstRow) + " already describes this entity (" + quote(firstName) +
		" — names are matched ignoring case and repeated spaces). An entity is matched by its name," +
		" and other rows refer to it by name, so the two rows cannot both be imported: merge them," +
		" or give one of them a name of its own."
}

// ErrDuplicateObligation refuses the second of two rows describing one
// obligation. (entity, obligation type) is the pairing the product has always
// treated as unique.
func ErrDuplicateObligation(entity, obligationType string, firstRow int) string {
	return "row " + strconv.Itoa(firstRow) + " already gives " + quote(entity) +
		" an obligation of type " + quote(obligationType) +
		". An entity has exactly one obligation per obligation type, so the two rows cannot both be" +
		" imported — merge them."
}

// ErrRowsSkipped names the rows that had something in them but nothing in any
// imported column — the totals line, the note under the table, the row someone
// cleared but did not delete. They are skipped, never silently.
func ErrRowsSkipped(rows []int) string {
	numbers := make([]string, 0, len(rows))
	for _, n := range rows {
		numbers = append(numbers, strconv.Itoa(n))
	}
	return pluralize(len(rows), "row", "rows") + " " + strings.Join(numbers, ", ") +
		" had nothing in any imported column and " + pluralize(len(rows), "was", "were") +
		" skipped — a totals line or a note under the table reads this way."
}

// ErrRowsAboveTheHeader names the rows that sit ABOVE the header row and were
// therefore not read. The same rows BELOW the header already get a warning
// (ErrRowsSkipped), and a row is no less lost for being above it: a sheet with
// two tables stacked on it loses the first one entirely, and the only trace is
// a row count the customer has nothing to compare against.
//
// It is a warning and not a refusal because the commonest thing above a header
// is a title and an export stamp, which are not data and are not meant to
// arrive. Saying so is what makes the header row the reader chose checkable —
// which is the only way a customer can tell the two cases apart.
func ErrRowsAboveTheHeader(headerRow int, rows []int) string {
	numbers := make([]string, 0, len(rows))
	for _, n := range rows {
		numbers = append(numbers, strconv.Itoa(n))
	}
	return "the column headings are on row " + strconv.Itoa(headerRow) + ", so " +
		pluralize(len(rows), "row", "rows") + " " + strings.Join(numbers, ", ") + " above " +
		pluralize(len(rows), "it was", "them were") + " not imported — a title, an export stamp" +
		" or a second table above the headings reads this way. If any of those rows is data," +
		" move it below the headings and upload the file again."
}

// ErrNoDataRows refuses a file whose header is the only thing in it.
func ErrNoDataRows(headerRow int) string {
	return "the column headings on row " + strconv.Itoa(headerRow) +
		" are the only thing in this file — there is nothing to import."
}

// ErrDeadlineStrategyAmbiguous refuses a row that fills both deadline
// strategies' columns without saying which one it means. The API takes a
// structured rule that has already chosen; a flat row has to say so.
func ErrDeadlineStrategyAmbiguous(fixed, offset []string) string {
	return "this row sets both fixed filing dates (" + strings.Join(fixed, ", ") +
		") and offsets after period end (" + strings.Join(offset, ", ") +
		"), which are two different ways to compute a deadline. Clear one set, or put " +
		quote("fixed") + " or " + quote("period_offset") + " in a " + quote("deadline type") + " column."
}

// ErrDeadlineStrategyUnused warns that a declared strategy left the other
// strategy's columns unread.
func ErrDeadlineStrategyUnused(declared string, unused []string) string {
	return "the deadline type is " + quote(declared) + ", so " + strings.Join(unused, ", ") +
		" " + pluralize(len(unused), "was", "were") + " not read."
}

// ErrDeadlineStrategyIncomplete refuses a row that declares a strategy and then
// does not supply the values it is made of.
func ErrDeadlineStrategyIncomplete(declared, needs string) string {
	return "the deadline type is " + quote(declared) + " but this row has no " + needs +
		" — fill them in, or clear the deadline type and leave the deadline rule to be set in ZenTax."
}

// ErrPaymentDatesMismatch refuses payment dates that do not pair with the
// filing dates: index i pays for filing i, so the counts have to match.
func ErrPaymentDatesMismatch(filing, payment int) string {
	return "there " + pluralize(payment, "is", "are") + " " + count(payment, "payment date") +
		" against " + count(filing, "filing date") +
		" — each filing date needs its own payment date, in the same order."
}

// The three deadline-rule remarks below are raised by the PLANNER rather than
// by the reader, because which one is true depends on what the row is about to
// do to a record — and only the planner can see the record. A row with no
// deadline columns is a new obligation with no schedule, an existing one whose
// schedule is left exactly as it is, or an existing one that never had a
// schedule, and those are three different things to tell a tax manager.

// ErrNoDeadlineRule warns about a NEW obligation with no deadline
// configuration. The API accepts one (the column defaults to an empty rule), so
// this cannot be a refusal — but a workflow started from it computes no
// deadlines.
func ErrNoDeadlineRule() string {
	return "no deadline rule on this row: the obligation will be recorded, but a workflow started" +
		" from it computes no filing or payment deadlines until a rule is set in ZenTax."
}

// ErrStillNoDeadlineRule is the same remark about an obligation that already
// exists: the file adds no rule and there was none to keep.
func ErrStillNoDeadlineRule() string {
	return "no deadline rule on this row, and this obligation has none in ZenTax either: a workflow" +
		" started from it computes no filing or payment deadlines until a rule is set."
}

// ErrDeadlineRuleKept is raised when a row fills part of the deadline columns
// without describing a complete rule, over an obligation that HAS one. The
// stored rule stands — an import never erases a filing calendar a file did not
// replace — and the row's stray deadline cells are not applied, which the
// customer has to be told rather than left to infer from a report that shows no
// change.
func ErrDeadlineRuleKept(rule string) string {
	return "this row's deadline columns do not describe a complete rule (a deadline type with its" +
		" dates, or a filing offset), so the rule already on this obligation — " + rule +
		" — is kept and those cells are not applied."
}

// ErrFixedDateCount warns when the number of fixed dates does not match what
// the periodicity implies. The API accepts any number, so this is a remark.
func ErrFixedDateCount(periodicity string, want, got int) string {
	return "a " + periodicity + " obligation usually has " + count(want, "fixed filing date") +
		", and this row has " + strconv.Itoa(got) + "."
}

// ErrNotACurrency refuses anything that is not an ISO 4217 alpha-3 code, which
// is what entityobligations/dto's `len=3,uppercase` asks for.
func ErrNotACurrency(value string) string {
	return quote(value) + " is not a currency code — write the three-letter ISO 4217 code," +
		" such as EUR, USD or GBP."
}

// ErrFileEmpty refuses a file with nothing in it.
func ErrFileEmpty() string {
	return "this file has no data in it."
}

// ErrSheetRead records which tab of a workbook was read, and why that one. It
// is reported for every workbook, difficult choice or not: a wrong-tab import
// produces a report in which every row is plausible — stale data looks exactly
// like data — so the name of the tab is the one line in the report that can
// give the mistake away.
func ErrSheetRead(name, because string) string {
	if because == "" {
		return "read from the sheet " + quote(name) + "."
	}
	return "read from the sheet " + quote(name) + " — " + because + "."
}

// ErrHiddenSheetNotRead names a hidden tab claiming the same import as the one
// that was read. A hidden sheet is last year's copy far more often than it is
// the register being maintained, so it never wins while a visible sheet is
// there — but a customer who meant it has to be told why.
func ErrHiddenSheetNotRead(names []string) string {
	return "this workbook also has " + pluralize(len(names), "a hidden sheet", "hidden sheets") +
		" named " + quoteList(names) + ", and a hidden sheet is never the one imported while there is a" +
		" visible one — " + pluralize(len(names), "unhide it", "unhide the one you meant") +
		" if that is the register you meant to send."
}

// ErrMergedCellsNotRead names merged blocks inside the table whose value could
// not be given back to the cells they cover. A merge holds its value in the
// top-left cell only; every other cell of the block comes back EMPTY, and an
// empty cell in a column the file includes is a statement that clears the
// stored value. A merge that runs down one column is filled in instead — that
// is a register saying "this value covers these rows" — so what is left here is
// the merges that run across columns, where filling would be inventing.
func ErrMergedCellsNotRead(refs []string, total int) string {
	named := strings.Join(refs, ", ")
	if total > len(refs) {
		named += " and " + strconv.Itoa(total-len(refs)) + " more"
	}
	return strconv.Itoa(total) + " merged " + pluralize(total, "block", "blocks") +
		" in this table " + pluralize(total, "spans", "span") + " more than one column (" + named +
		"). A merged block holds its value in its top-left cell only, so the cells to the right of it were" +
		" read as empty — and an empty cell in a column the file includes clears what is stored." +
		" Unmerge those cells and write the value in each column it belongs to."
}

// ErrSheetAmbiguous refuses a workbook with two tabs claiming the same target.
func ErrSheetAmbiguous(target Target, names []string) string {
	return "this workbook has more than one " + target.Label() + " sheet (" + quoteList(names) +
		") — import them one at a time."
}

// ErrSheetUnnamed refuses a workbook whose tabs do not say which one to read.
func ErrSheetUnnamed(target Target, names []string) string {
	return "this workbook has several sheets with data (" + quoteList(names) +
		") and none of them is named for " + target.Label() +
		" — rename the one to import, or upload it on its own."
}

// ErrUnknownTarget refuses an import of something this wave does not import.
func ErrUnknownTarget(target Target) string {
	return quote(string(target)) + " is not something ZenTax imports — import " +
		quote(string(TargetEntities)) + " or " + quote(string(TargetObligations)) +
		". Workflows and task instances are generated from those, not imported."
}

// countUnit is what a numeric deadline column counts, taken from the field name
// so the two offset pairs never need their own message.
func countUnit(field string) string {
	if strings.HasSuffix(field, "Days") {
		return "days"
	}
	return "months"
}

// count writes "1 filing date" and "2 filing dates".
func count(n int, noun string) string {
	return strconv.Itoa(n) + " " + noun + pluralize(n, "", "s")
}

func quote(s string) string { return "\"" + s + "\"" }

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, quote(item))
	}
	return strings.Join(quoted, ", ")
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
