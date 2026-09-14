package handlers

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/parsing"
)

// The blank sheet is the one artefact a customer builds their file FROM, so the
// only thing that can be wrong with it is a header the parser then refuses. The
// rule these tests hold is therefore narrow and total: whatever Fields() says,
// the sheet says, and the sheet binds back.

// mark is the UTF-8 byte-order mark the sheet opens with. Spelled as an escape
// rather than as itself, because the literal is invisible in a diff.
const mark = "\uFEFF"

// headerCells is the sheet's single header row, read the way the product's own
// reader reads it: the mark consumed first, then encoding/csv.
func headerCells(t *testing.T, sheet []byte) []string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(sheet), mark))).ReadAll()
	if err != nil {
		t.Fatalf("the sheet is not readable as CSV: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("a blank sheet is one header row and no data rows, got %d rows", len(records))
	}
	return records[0]
}

func TestTemplateSheetIsOneHeaderRowOfCanonicalNames(t *testing.T) {
	for _, kind := range []domain.Kind{domain.KindEntities, domain.KindEntityObligations} {
		fields := domain.Fields(kind.Target())
		got := headerCells(t, TemplateSheet(fields))

		want := make([]string, 0, len(fields))
		for _, f := range fields {
			want = append(want, f.Name)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: header row is %q, want %q", kind, got, want)
		}
	}
}

// THE SHEET DECLARES ITS ENCODING, and this is the assertion that keeps it
// doing so. Every heading is ASCII, so the mark changes nothing about the file
// as downloaded — it is there for the file as SENT BACK. A spreadsheet saves a
// CSV in whatever encoding it decided the file was, and a file that declares
// nothing is saved in the machine's own ANSI codepage; the customer then
// returns bytes whose meaning depends on their laptop's locale. Reading a name
// typed as "Łódź Spółka" out of Windows-1250 bytes as Windows-1252 yields
// "£ódŸ Spó³ka", and no care on the reading side can recover it, because by
// then the original bytes are gone. Three bytes here are what stop that.
func TestTemplateSheetDeclaresUTF8(t *testing.T) {
	for _, kind := range []domain.Kind{domain.KindEntities, domain.KindEntityObligations} {
		sheet := TemplateSheet(domain.Fields(kind.Target()))
		if got := sheet[:3]; string(got) != mark {
			t.Errorf("%s: the sheet opens with % x, want the UTF-8 byte-order mark ef bb bf", kind, got)
		}
		// Exactly one. A second mark is a character in the first heading.
		if n := strings.Count(string(sheet), mark); n != 1 {
			t.Errorf("%s: the sheet carries %d byte-order marks, want 1", kind, n)
		}
	}
}

// Every column the parser knows must be ON the sheet — including the ones a
// customer would never invent. The obligations sheet lost its whole deadline
// block once, in a hand-written column list, and the file that produced was a
// filing calendar with no dates in it.
func TestTemplateSheetCarriesEveryImportableColumn(t *testing.T) {
	sheet := string(TemplateSheet(domain.Fields(domain.TargetObligations)))
	for _, field := range []string{
		domain.FieldDeadlineType, domain.FieldPeriodStart,
		domain.FieldFilingOffsetMonths, domain.FieldFilingOffsetDays,
		domain.FieldPaymentOffsetMonths, domain.FieldPaymentOffsetDays,
		domain.FieldFixedDates, domain.FieldPaymentFixedDates, domain.FieldWeekendAdjustment,
	} {
		if !strings.Contains(sheet, field) {
			t.Errorf("the obligations sheet has no %q column, so a file built from it sets no deadline rule", field)
		}
	}
}

// The sheet the product hands out must be a sheet the product can read. This is
// the same invariant domain.TestEveryFieldNameBindsAsItsOwnHeader states about
// the field list; here it is asserted against the bytes actually written, so a
// future generator that decorates a header ("name *", "Name (required)") is
// caught by the test rather than by a customer.
func TestTheSheetBindsBackToTheFieldsItWasBuiltFrom(t *testing.T) {
	for _, kind := range []domain.Kind{domain.KindEntities, domain.KindEntityObligations} {
		target := kind.Target()
		for _, header := range headerCells(t, TemplateSheet(domain.Fields(target))) {
			field, ok := domain.FieldForHeader(target, header)
			if !ok {
				t.Errorf("%s: the sheet's own header %q binds to no field", kind, header)
				continue
			}
			if field != header {
				t.Errorf("%s: the sheet's header %q binds to %q instead", kind, header, field)
			}
		}
	}
}

// And the whole path in one test, which is the only way it really settles: the
// bytes the download route writes, filled in the way a customer fills them,
// read back by the reader the upload route uses.
//
// It is the mark that makes this worth asserting beyond the byte check above.
// Consumed, the sheet reads as it always did; read as text, the first heading
// becomes "\uFEFFname", binds to nothing, and a file built from the product's
// own sheet is refused for missing the one column it cannot do without. The
// value carried through is deliberately one that no single-byte codepage
// agrees on, so a regression that drops the declaration shows up here as a
// changed name rather than as nothing at all.
func TestTheSheetAsDownloadedAndFilledInIsReadBackUnchanged(t *testing.T) {
	const typed = "Łódź Spółka z o.o."

	filled := map[domain.Target]map[string]string{
		domain.TargetEntities: {
			domain.FieldName:    typed,
			domain.FieldCountry: "PL",
		},
		domain.TargetObligations: {
			domain.FieldEntity:             typed,
			domain.FieldObligationType:     "VAT",
			domain.FieldTaxReferenceNumber: "0012345678",
			domain.FieldPeriodicity:        "annual",
		},
	}

	for _, kind := range []domain.Kind{domain.KindEntities, domain.KindEntityObligations} {
		target := kind.Target()
		fields := domain.Fields(target)

		// The sheet exactly as handed out, plus one row under its headings.
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = filled[target][f.Name]
		}
		sheet := append(TemplateSheet(fields), []byte(strings.Join(row, ",")+"\r\n")...)

		outcome, err := parsing.New().Read(context.Background(), domain.Upload{
			Target:   target,
			FileName: TemplateFileName(kind),
			Content:  sheet,
			MaxRows:  1000,
		})
		if err != nil {
			t.Fatalf("%s: the sheet the product hands out cannot be read back: %v", kind, err)
		}
		for _, issue := range outcome.FileIssues {
			if issue.IsError() {
				t.Errorf("%s: reading the filled-in sheet raised %q", kind, issue.Message)
			}
		}
		// Every column bound, under its own name, and none left over as one the
		// import does not recognise, which is what a mark read as text would turn
		// the first column into.
		for _, f := range fields {
			if got := outcome.Columns[f.Name]; got != f.Name {
				t.Errorf("%s: column %q bound to header %q when the sheet was read back", kind, f.Name, got)
			}
		}
		if len(outcome.Ignored) != 0 {
			t.Errorf("%s: the product's own sheet has columns it does not recognise: %+v", kind, outcome.Ignored)
		}

		if len(outcome.Rows) != 1 {
			t.Fatalf("%s: read %d rows from a sheet holding one; file issues %+v",
				kind, len(outcome.Rows), outcome.FileIssues)
		}
		draft := outcome.Rows[0]
		for _, issue := range draft.Issues {
			if issue.IsError() {
				t.Errorf("%s: the filled-in row was refused: %q", kind, issue.Message)
			}
		}
		var got string
		switch {
		case draft.Entity != nil:
			got = draft.Entity.Name
		case draft.Obligation != nil:
			got = draft.Obligation.EntityRef
		default:
			t.Fatalf("%s: the row read as neither an entity nor an obligation", kind)
		}
		if got != typed {
			t.Errorf("%s: the customer typed %q and the reader read %q", kind, typed, got)
		}
	}
}

func TestTemplateFileNameNamesTheProductAndTheKind(t *testing.T) {
	if got := TemplateFileName(domain.KindEntities); got != "zentax-entities-template.csv" {
		t.Errorf("entities sheet is offered as %q", got)
	}
	// The stored kind spells itself with an underscore; the file name follows
	// the URL target, which is what the customer saw when they clicked.
	if got := TemplateFileName(domain.KindEntityObligations); got != "zentax-entity-obligations-template.csv" {
		t.Errorf("obligations sheet is offered as %q", got)
	}
}
