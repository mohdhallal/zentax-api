package domain

import (
	"math"
	"sort"
)

// MaxRows is the default cap on the data rows one import may carry, used when
// an upload names none. It is a working bound, not a licence limit: a bigger
// tax book is imported in parts, which also keeps the dry run readable and the
// commit's transaction short.
const MaxRows = 10000

// Read is the reading half's whole job: find the header, bind the columns, and
// validate every row against the rules the single-record routes enforce.
//
// It never stops at the first problem. A customer with a thousand-row tax book
// needs the whole list of what is wrong so they can fix it in one pass; a dry
// run that reports one error per upload is a dry run nobody uses.
func Read(sheet Sheet, target Target, maxRows int) (*ReadOutcome, error) {
	if maxRows <= 0 {
		maxRows = MaxRows
	}
	outcome := &ReadOutcome{Rows: []DraftRow{}}

	if !target.Valid() {
		outcome.FileIssues = append(outcome.FileIssues, FileError(ErrUnknownTarget(target)))
		return outcome, nil
	}
	if sheet.IsEmpty() {
		outcome.FileIssues = append(outcome.FileIssues, FileError(ErrFileEmpty()))
		return outcome, nil
	}

	binding, ignored, issues := BindHeader(sheet, target)
	outcome.Ignored = ignored
	outcome.FileIssues = append(outcome.FileIssues, issues...)
	if binding == nil {
		return outcome, nil
	}
	outcome.HeaderRow = binding.HeaderRow
	outcome.Columns = binding.Columns()

	var skipped, above []int
	firstSeenAt := map[string]seenRow{}
	for _, row := range sheet.Rows {
		if row.Number <= binding.HeaderRow {
			// A row ABOVE the headings is not read — the header row is where
			// the table starts. Usually that is a title and an export stamp;
			// sometimes it is a whole first table on the same sheet, and then
			// every row of it is gone. The same skip below the headings is
			// already reported by name, and this one costs no less.
			if row.Number < binding.HeaderRow && !row.IsBlank() {
				above = append(above, row.Number)
			}
			continue
		}
		if isBlankForBinding(binding, target, row) {
			if !row.IsBlank() {
				skipped = append(skipped, row.Number)
			}
			continue
		}
		if len(outcome.Rows) >= maxRows {
			// The port's contract: nothing past the cap is parsed, and the file
			// is refused outright rather than half-reported (ErrTooManyRows).
			return nil, ErrTooManyRows
		}
		outcome.Rows = append(outcome.Rows, readRow(binding, row, target, firstSeenAt))
	}

	if len(above) > 0 {
		outcome.FileIssues = append(outcome.FileIssues,
			FileWarning(ErrRowsAboveTheHeader(binding.HeaderRow, above)))
	}
	if len(skipped) > 0 {
		outcome.FileIssues = append(outcome.FileIssues, FileWarning(ErrRowsSkipped(skipped)))
	}
	if len(outcome.Rows) == 0 && !outcome.HasFileError() {
		outcome.FileIssues = append(outcome.FileIssues, FileError(ErrNoDataRows(binding.HeaderRow)))
	}
	return outcome, nil
}

// seenRow is where a natural key was first claimed, and how that row spelled
// it — a message about a repeat has to quote the row it collides with, not the
// row it is on, or the customer goes looking for the wrong spelling.
type seenRow struct {
	number int
	label  string
}

// readRow reads one data row and checks it against the rows before it: the same
// natural key twice in one file has no single meaning, so the second occurrence
// is refused rather than silently overwriting the first at commit time.
func readRow(binding *Binding, row Row, target Target, firstSeenAt map[string]seenRow) DraftRow {
	out := DraftRow{Number: row.Number}
	switch target {
	case TargetObligations:
		draft, issues := readObligationRow(binding, row)
		out.Obligation, out.Issues = draft, issues
		if draft.EntityRef != "" && draft.ObligationTypeRef != "" {
			if first, seen := firstSeenAt[draft.NaturalKey()]; seen {
				out.DuplicateOfRow = first.number
				out.Issues = append(out.Issues, RowError(binding, row, FieldObligationType, draft.ObligationTypeRef,
					ErrDuplicateObligation(first.label, draft.ObligationTypeRef, first.number)))
			} else {
				firstSeenAt[draft.NaturalKey()] = seenRow{number: row.Number, label: draft.EntityRef}
			}
		}
	case TargetEntities:
		draft, issues := readEntityRow(binding, row)
		out.Entity, out.Issues = draft, issues
		if draft.Name != "" {
			if first, seen := firstSeenAt[draft.NaturalKey()]; seen {
				out.DuplicateOfRow = first.number
				out.Issues = append(out.Issues, RowError(binding, row, FieldName, draft.Name,
					ErrDuplicateEntity(first.label, first.number)))
			} else {
				firstSeenAt[draft.NaturalKey()] = seenRow{number: row.Number, label: draft.Name}
			}
		}
	}
	sortIssuesByColumn(binding, out.Issues)
	return out
}

// sortIssuesByColumn puts a row's problems in the order the customer reads
// their own row — left to right — rather than in the order this package happens
// to check them. Issues with no column of their own (a rule assembled from
// several columns) come last.
func sortIssuesByColumn(binding *Binding, issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		return issueColumn(binding, issues[i]) < issueColumn(binding, issues[j])
	})
}

func issueColumn(binding *Binding, issue Issue) int {
	if col, ok := binding.Index(issue.Field); ok {
		return col
	}
	return math.MaxInt
}

// isBlankForBinding reports whether a row says nothing in any column this
// import reads. A real export ends in a totals line, a footnote, a row somebody
// cleared but did not delete; none of them is an entity, and refusing the file
// over one would be absurd — but nor are they dropped in silence.
func isBlankForBinding(binding *Binding, target Target, row Row) bool {
	for _, f := range Fields(target) {
		if col, ok := binding.Index(f.Name); ok && CleanCell(row.Cell(col)) != "" {
			return false
		}
	}
	return true
}
