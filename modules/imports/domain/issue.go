package domain

// Severity decides whether a commit may proceed. A single error anywhere
// refuses the whole file; a warning never does.
type Severity string

const (
	// SeverityError is a value the single-record route would reject, or a file
	// this package cannot read without guessing. It blocks the commit.
	SeverityError Severity = "error"
	// SeverityWarning is legal but probably not meant. It is reported and the
	// import proceeds.
	SeverityWarning Severity = "warning"
)

// maxIssueValueRunes bounds the echo of a cell's content in a message, so one
// pathological cell cannot make a report unreadable (or large).
const maxIssueValueRunes = 80

// Issue is one thing wrong with — or worth knowing about — an import file,
// addressed the way a spreadsheet addresses things: the row number the
// customer sees, the header they wrote, and the cell they can click on. Row 0
// means the issue is about the file as a whole.
type Issue struct {
	Severity Severity `json:"severity"`
	Row      int      `json:"row,omitempty"`
	CellRef  string   `json:"cellRef,omitempty"`
	Column   string   `json:"column,omitempty"`
	Field    string   `json:"field,omitempty"`
	Value    string   `json:"value,omitempty"`
	Message  string   `json:"message"`
}

// IsError reports whether this issue blocks a commit.
func (i Issue) IsError() bool { return i.Severity == SeverityError }

// FileError is an issue about the file rather than any one row.
func FileError(message string) Issue {
	return Issue{Severity: SeverityError, Message: message}
}

// FileWarning is a file-wide remark that does not block the commit.
func FileWarning(message string) Issue {
	return Issue{Severity: SeverityWarning, Message: message}
}

// cellIssue addresses an issue at the cell a field was read from. col < 0 means
// the field has no column in this file (a default was used, or the column is
// missing), in which case only the row is named.
func cellIssue(sev Severity, b *Binding, row Row, field, value, message string) Issue {
	issue := Issue{
		Severity: sev,
		Row:      row.Number,
		Field:    field,
		Value:    truncate(value),
		Message:  message,
	}
	if b != nil {
		if col, ok := b.Index(field); ok {
			issue.CellRef = CellRef(col, row.Number)
			issue.Column = b.Header(field)
		}
	}
	return issue
}

// RowError is a cell-addressed refusal.
func RowError(b *Binding, row Row, field, value, message string) Issue {
	return cellIssue(SeverityError, b, row, field, value, message)
}

// RowWarning is a cell-addressed remark that does not block the commit.
func RowWarning(b *Binding, row Row, field, value, message string) Issue {
	return cellIssue(SeverityWarning, b, row, field, value, message)
}

func truncate(s string) string {
	runes := []rune(s)
	if len(runes) <= maxIssueValueRunes {
		return s
	}
	return string(runes[:maxIssueValueRunes]) + "…"
}
