package parsing

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

var _ domain.FileReader = (*Reader)(nil)

// Read is the whole reading half in one call, and the seam the committing half
// sees: sniff the bytes, choose the sheet, validate every row.
//
// An error comes back only when there is nothing to read at all — a legacy
// workbook, a password, bytes that are not a table in any format. Everything a
// file merely gets WRONG comes back inside the outcome, because a customer
// fixing a file needs the whole list at once and an error carries one line.
//
// Nothing here reads or writes the database. Resolving a natural key to a
// record, checking the requester's capability and entity scope, and recording
// what happened all belong to the committing half.
func (r *Reader) Read(_ context.Context, upload domain.Upload) (*domain.ReadOutcome, error) {
	file, err := r.ReadFile(upload.FileName, upload.Content)
	if err != nil {
		return nil, err
	}

	sheet, issues := file.PickSheet(upload.Target)
	notes := append(append([]domain.Issue{}, file.Notes...), issues...)
	if len(sheet.Rows) == 0 {
		return &domain.ReadOutcome{FileIssues: notes, Rows: []domain.DraftRow{}}, nil
	}
	// Nothing that is not text gets past here, whatever the format: a NUL in a
	// cell is a file read as the wrong encoding, and it is refused by cell
	// rather than left to fail much later as a database error.
	if err := checkControlCharacters(sheet); err != nil {
		return nil, err
	}

	// Where the table starts is settled by the half that knows the target's
	// column names, and everything below is only right once it is known: a
	// merged block is expanded under the headings rather than through them, and
	// a row's width is judged against the headings rather than against the
	// widest row in the file.
	headerRow := domain.HeaderRowOf(sheet, upload.Target)
	if headerRow > 0 {
		expandMergedValues(&sheet, headerRow)
		if refs := mergedColumnSpans(sheet, headerRow); len(refs) > 0 {
			notes = append(notes, domain.FileWarning(
				domain.ErrMergedCellsNotRead(firstRefs(refs, namedMergedRanges), len(refs))))
		}
	}

	outcome, err := domain.Read(sheet, upload.Target, upload.MaxRows)
	if err != nil {
		return nil, err
	}
	outcome.FileIssues = append(notes, outcome.FileIssues...)
	if issue := checkRowWidth(sheet, outcome.HeaderRow); issue != nil {
		outcome.FileIssues = append(outcome.FileIssues, *issue)
	}
	// And the same defect where it leaves no trace in the table's shape: a cell
	// split by the file's own separator, with blank columns after it.
	if issue := checkRowShift(sheet, outcome.HeaderRow); issue != nil {
		outcome.FileIssues = append(outcome.FileIssues, *issue)
	}
	return outcome, nil
}

// namedMergedRanges is how many merged blocks a warning lists by reference
// before it counts the rest.
const namedMergedRanges = 5

// firstRefs is the first n of a list, for a message that names a few and counts
// the rest.
func firstRefs(refs []string, n int) []string {
	if len(refs) <= n {
		return refs
	}
	return refs[:n]
}
