package domain

// MaxHeaderScanRows is how far into a sheet the header row is looked for. Real
// exports put a report title, a "generated on" stamp, a company logo's merged
// row and a blank line above their header; none of them run to twenty-five
// rows, and scanning further would start finding headers inside data.
const MaxHeaderScanRows = 25

// minHeaderMatches is how many distinct fields a row must name before it is
// believed to be the header. One is too few — a data row whose first cell reads
// "Country" would win — unless the target only has one importable column.
const minHeaderMatches = 2

// IgnoredColumn is a column this import does not know. It is neither imported
// nor grounds for refusing the file: real exports carry an owner, a review
// note, last year's numbers. Naming it in the report is the honest middle — the
// customer sees exactly what will not arrive, instead of believing it did.
type IgnoredColumn struct {
	Column string `json:"column"`
	Header string `json:"header"`
}

// NoHeading is the header an ignored column carries when the header cell itself
// is empty. Such a column used to be skipped before the bookkeeping below ever
// ran, so a column full of values was dropped AND was absent from the one list
// that would have shown it. A heading merged across two columns produces
// exactly that shape, which makes it a filing habit rather than a mistake. A
// blank-headed column with nothing under it stays silent: a spacer column is
// not a loss.
const NoHeading = "(no heading)"

// Binding is the map from this file's columns to the target's fields, together
// with the header text the customer wrote for each — a message quotes their
// spelling, not ours.
type Binding struct {
	// HeaderRow is the 1-based source row the header was found on.
	HeaderRow int
	columns   map[string]int
	headers   map[string]string
}

// Index is the 0-based column a field was bound to.
func (b *Binding) Index(field string) (int, bool) {
	col, ok := b.columns[field]
	return col, ok
}

// IndexOrMissing is Index for the readers, which treat an absent column exactly
// as they treat an empty cell.
func (b *Binding) IndexOrMissing(field string) int {
	if col, ok := b.columns[field]; ok {
		return col
	}
	return -1
}

// Has reports whether the file carries a column for the field at all.
func (b *Binding) Has(field string) bool {
	_, ok := b.columns[field]
	return ok
}

// Header is the header text the file wrote for a field, for quoting back.
func (b *Binding) Header(field string) string { return b.headers[field] }

// Columns is the field-to-header map, for the report's "this is what I read
// your file as" summary.
func (b *Binding) Columns() map[string]string {
	out := make(map[string]string, len(b.headers))
	for field, header := range b.headers {
		out[field] = header
	}
	return out
}

// BindHeader finds the header row and maps its columns onto the target's
// fields. It returns nil when the file cannot be bound at all, in which case
// the issues say why and no row is read: a file whose columns are not
// understood has nothing worth reporting row by row.
func BindHeader(sheet Sheet, target Target) (*Binding, []IgnoredColumn, []Issue) {
	headerIdx, matched := locateHeaderRow(sheet, target)
	if headerIdx < 0 {
		return nil, nil, []Issue{FileError(ErrNoHeaderRow(target))}
	}

	header := sheet.Rows[headerIdx]
	binding := &Binding{
		HeaderRow: header.Number,
		columns:   make(map[string]int, matched),
		headers:   make(map[string]string, matched),
	}

	var (
		issues  []Issue
		ignored []IgnoredColumn
		claimed = map[string]int{}
	)
	// Columns are walked to the width of the TABLE and not of the header row:
	// a header row ends at its last heading, so a column whose heading is
	// missing altogether is not represented in it at all, and a column nobody
	// can see is a column nobody can be told about.
	unheaded := columnsWithNoHeading(sheet, headerIdx)
	width := tableWidth(sheet, headerIdx)
	for col := 0; col < width; col++ {
		text := CleanCell(header.Cell(col))
		if text == "" {
			if unheaded[col] {
				ignored = append(ignored, IgnoredColumn{Column: ColumnLetter(col), Header: NoHeading})
			}
			continue
		}
		field, ok := FieldForHeader(target, text)
		if !ok {
			ignored = append(ignored, IgnoredColumn{Column: ColumnLetter(col), Header: text})
			continue
		}
		if first, dup := claimed[field]; dup {
			issues = append(issues, FileError(ErrColumnClaimedTwice(
				canonicalHeaders[field], ColumnLetter(first), binding.headers[field], ColumnLetter(col), text)))
			continue
		}
		claimed[field] = col
		binding.columns[field] = col
		binding.headers[field] = text
	}

	// Only the KEY columns are required of the file. A file without them names
	// no record and there is nothing to read the rest from, so it is refused
	// whole. Every other required column is required of a row that CREATES —
	// raised per row by the planner, which is the half that can see whether a row
	// creates or corrects (MissingOnCreate). A correction sheet carrying the key
	// and the one column being corrected is a file this import accepts.
	for _, f := range Fields(target) {
		if f.Key && !binding.Has(f.Name) {
			issues = append(issues, FileError(ErrRequiredColumnMissing(f)))
		}
	}
	if len(ignored) > 0 {
		issues = append(issues, FileWarning(ErrColumnsNotImported(target, ignored)))
	}
	for _, issue := range issues {
		if issue.IsError() {
			return nil, ignored, issues
		}
	}
	return binding, ignored, issues
}

// columnsWithNoHeading reports the header row's empty cells that have a value
// under them somewhere in the file. A column with a blank heading is not
// imported — there is nothing to bind it by — but whether that costs the
// customer anything depends entirely on what is underneath it, and only the
// ones that cost something are worth a line in the report.
//
// Only rows BELOW the header are looked at: a title block above it is not this
// column's data.
func columnsWithNoHeading(sheet Sheet, headerIdx int) map[int]bool {
	header := sheet.Rows[headerIdx]
	width := tableWidth(sheet, headerIdx)
	blanks := map[int]bool{}
	for col := 0; col < width; col++ {
		if CleanCell(header.Cell(col)) == "" {
			blanks[col] = true
		}
	}
	if len(blanks) == 0 {
		return nil
	}

	found := map[int]bool{}
	for i := headerIdx + 1; i < len(sheet.Rows) && len(found) < len(blanks); i++ {
		for col := range blanks {
			if found[col] {
				continue
			}
			if CleanCell(sheet.Rows[i].Cell(col)) != "" {
				found[col] = true
			}
		}
	}
	return found
}

// tableWidth is how many columns the table occupies: the header row's own
// cells, and any column further right that a row under it fills. The two differ
// only when a heading is missing, which is exactly the case worth reporting.
func tableWidth(sheet Sheet, headerIdx int) int {
	width := len(sheet.Rows[headerIdx].Cells)
	for i := headerIdx + 1; i < len(sheet.Rows); i++ {
		if n := len(sheet.Rows[i].Cells); n > width {
			width = n
		}
	}
	return width
}

// HeaderRowOf is the header row this target would be read under, as a 1-based
// row number, or 0 when the file's columns cannot be recognised at all. The
// parsing half asks for it before it hands the sheet over, because two of the
// things it has to do — expanding merged cells, judging a row's width — are
// only correct once the table's first row is known, and the answer belongs to
// the half that knows the target's column names.
func HeaderRowOf(sheet Sheet, target Target) int {
	idx, _ := locateHeaderRow(sheet, target)
	if idx < 0 {
		return 0
	}
	return sheet.Rows[idx].Number
}

// locateHeaderRow picks the row that names the most of the target's fields,
// within the scan window and earliest-wins on a tie. Scoring the whole window
// rather than taking the first line is what lets a file keep its title block:
// the header is wherever the field names actually are.
func locateHeaderRow(sheet Sheet, target Target) (index, matched int) {
	limit := len(sheet.Rows)
	if limit > MaxHeaderScanRows {
		limit = MaxHeaderScanRows
	}
	threshold := minHeaderMatches
	if len(Fields(target)) < threshold {
		threshold = len(Fields(target))
	}

	best, bestScore := -1, 0
	for i := 0; i < limit; i++ {
		seen := map[string]bool{}
		for _, cell := range sheet.Rows[i].Cells {
			if field, ok := FieldForHeader(target, CleanCell(cell)); ok {
				seen[field] = true
			}
		}
		if len(seen) > bestScore {
			best, bestScore = i, len(seen)
		}
	}
	if bestScore < threshold {
		return -1, 0
	}
	return best, bestScore
}

// MissingOnCreate is the refusal of a row that would create a record while the
// file carries no column for something a new record cannot do without.
//
// It is a ROW error rather than a file one, and only on a create: the same file
// corrects existing records perfectly well, because a column it does not have
// is a column the customer said nothing about and the stored value answers for
// it. What cannot be done is invent a country, or a filing frequency, for a
// record that does not exist yet — so those rows are refused by name, and the
// rest of the file still imports.
func MissingOnCreate(target Target, rowNumber int, spoke map[string]bool) []Issue {
	var out []Issue
	for _, f := range Fields(target) {
		if !f.Required || f.Key || spoke[f.Name] {
			continue
		}
		out = append(out, Issue{
			Severity: SeverityError, Row: rowNumber, Field: f.Name,
			Message: ErrRequiredColumnMissingForCreate(target, f),
		})
	}
	return out
}

// spokenFields is the set of a target's fields the file actually has a column
// for. It is read once per file from the binding, and carried on every draft,
// so the planner never has to ask "was that column there?" after the file is
// gone — which it is, by the time a commit runs.
func spokenFields(b *Binding, target Target) map[string]bool {
	spoke := make(map[string]bool)
	for _, f := range Fields(target) {
		if b.Has(f.Name) {
			spoke[f.Name] = true
		}
	}
	return spoke
}
