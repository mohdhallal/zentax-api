//go:build ignore

// generate.go writes the .xlsx fixtures by hand, as raw OOXML in a zip, rather
// than with the library the reader uses. A fixture written by the reader's own
// dependency proves only that the dependency round-trips; one written the way
// another tool writes — shared strings, an inline string, a built-in date
// format and a custom one, a title block above the header, a spacer column —
// proves the reader copes with somebody else's workbook.
//
// Run it from this directory: go run generate.go
package main

import (
	"archive/zip"
	"fmt"
	"os"
	"strings"
	"time"
)

// excelEpoch is the day Excel's serial 0 falls on in the 1900 date system.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

func serial(date string) int {
	when, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return int(when.Sub(excelEpoch).Hours() / 24)
}

// cell kinds.
type cell struct {
	shared   int // index into the shared string table, or -1
	inline   string
	number   string
	styleIdx int

	// formula is the <f> a cell carries; cached is the <v> its workbook saved
	// alongside it, and cachedType the t= that goes with that value. A formula
	// with no cached value is what a generated workbook writes, and what a
	// workbook saved with "calculate on load" carries.
	formula    string
	cached     string
	cachedType string
}

func shared(i int) cell               { return cell{shared: i, styleIdx: 0} }
func inline(s string) cell            { return cell{shared: -1, inline: s, styleIdx: 0} }
func number(s string, style int) cell { return cell{shared: -1, number: s, styleIdx: style} }
func date(d string, s int) cell       { return cell{shared: -1, number: fmt.Sprint(serial(d)), styleIdx: s} }
func blank() cell                     { return cell{shared: -1, styleIdx: 0} }

// formula is a formula cell whose workbook saved its result.
func formula(f, cached string) cell {
	return cell{shared: -1, formula: f, cached: cached, cachedType: "str"}
}

// uncalculated is a formula cell whose workbook saved no result at all.
func uncalculated(f string) cell { return cell{shared: -1, formula: f} }

// lookupFailed is a formula cell whose saved result is the spreadsheet's own
// error text — a lookup that found nothing.
func lookupFailed(f, code string) cell {
	return cell{shared: -1, formula: f, cached: code, cachedType: "e"}
}

var strings4 = []string{
	"Global Tax Compliance — Group Entity Register",
	"Exported from the group consolidation system on 03/09/2026",
	"Entity Name",
	"Legal name",
	"Country of incorporation",
	"Parent company",
	"Financial Year End (MM-DD)",
	"Fiscal calendar",
	"Tax residency",
	"Responsible",
	"Acme Holding AG",
	"Acme Holding Aktiengesellschaft",
	"Switzerland",
	"Standard",
	"A. Meier",
	"Acme Deutschland GmbH",
	"Acme Deutschland Gesellschaft mit beschränkter Haftung",
	"Germany",
	"K. Schmidt",
	"Acme France SAS",
	"Acme France Société par actions simplifiée",
	"France",
	"4-4-5",
	"L. Dubois",
	"Acme UK Limited",
	"United Kingdom",
	"J. Hart",
	"Total entities: 4",
	"Prepared by the group tax team.",
	"Figures are unaudited.",
	"Obligation type",
	"Tax reference number",
	"Filing frequency",
	"Filing offset days",
	"VAT-RET",
	"monthly",
	"quarterly",
	"Müller Holding GmbH",
	"Müller Vertrieb GmbH",
	"Müller Süd GmbH",
	"Germany",
	"12-31",
	"Acme Holding Aktiengesellschaft",
	"Lookups",
}

// Indexes into strings4 for the obligations sheet, named so the layout below
// reads as a sheet rather than as arithmetic.
const (
	sEntity          = 2
	sObligationType  = 30
	sTaxReference    = 31
	sFilingFrequency = 32
	sFilingDays      = 33
	sVatRet          = 34
	sMonthly         = 35
	sQuarterly       = 36
	sAcmeDE          = 15
	sAcmeFR          = 19

	// The formula workbooks' own strings.
	sMuellerHolding   = 37
	sMuellerVertrieb  = 38
	sMuellerSued      = 39
	sGermany          = 40
	sYearEnd          = 41
	sAcmeHoldingAG    = 10
	sAcmeHoldingLegal = 42
	sLegalName        = 3
	sCountry          = 4
	sFinancialYear    = 6
	sSwitzerland      = 12
)

func main() {
	// Sheet 1: the register, with a title block, a spacer column between
	// "Fiscal calendar" and "Tax residency", a blank line in the middle, a
	// year end held as a real date cell, another as text, and a totals row.
	sheet1 := [][]cell{
		{shared(0)},
		{shared(1)},
		{},
		{shared(2), shared(3), shared(4), shared(5), shared(6), shared(7), blank(), shared(8), shared(9)},
		{shared(10), shared(11), shared(12), blank(), date("2026-12-31", 1), shared(13), blank(), shared(12), shared(14)},
		{shared(15), shared(16), shared(17), shared(10), date("2026-12-31", 2), shared(13), blank(), shared(17), shared(18)},
		{},
		{shared(19), shared(20), shared(21), shared(10), inline(" 12-31 "), shared(22), blank(), shared(21), shared(23)},
		{shared(24), blank(), shared(25), shared(10), inline("5 April"), shared(13), blank(), shared(25), shared(26)},
		{},
		{blank(), blank(), blank(), blank(), blank(), blank(), blank(), blank(), shared(27)},
	}
	sheet2 := [][]cell{
		{shared(28)},
		{shared(29)},
	}

	write("entities_group_register.xlsx", map[string][][]cell{
		"Entities": sheet1,
		"Notes":    sheet2,
	}, []string{"Entities", "Notes"})

	// A register whose tax reference numbers were typed as NUMBERS — which is
	// what happens to every all-digit reference in every spreadsheet. One of
	// them (45657) falls inside the Excel date-serial range, and one is stored
	// in scientific notation. Neither may come out as a date, and neither may
	// come out as "1.2345678901e+10".
	obligations := [][]cell{
		{shared(sEntity), shared(sObligationType), shared(sTaxReference), shared(sFilingFrequency), shared(sFilingDays)},
		{shared(sAcmeDE), shared(sVatRet), number("45657", 3), shared(sMonthly), number("10", 0)},
		{shared(sAcmeFR), shared(sVatRet), number("1.2345678901E+10", 0), shared(sQuarterly), number("30", 0)},
	}
	write("obligations_typed_as_numbers.xlsx", map[string][][]cell{
		"Obligations": obligations,
	}, []string{"Obligations"})

	// A register whose legal-name column is a formula. Row 2's formula was
	// saved with its result and row 3's was not — which is what a workbook
	// written by a generator, or saved with "calculate on load", carries. Read
	// raw, the second is indistinguishable from a cell somebody cleared.
	unsaved := [][]cell{
		{shared(sEntity), shared(sLegalName), shared(sCountry), shared(sFinancialYear)},
		{shared(sMuellerHolding), formula(`A2&" Gesellschaft mbH"`, "Müller Holding GmbH Gesellschaft mbH"),
			shared(sGermany), shared(sYearEnd)},
		{shared(sMuellerVertrieb), uncalculated(`A3&" Gesellschaft mbH"`), shared(sGermany), shared(sYearEnd)},
		{shared(sMuellerSued), formula(`A4&" Gesellschaft mbH"`, "Müller Süd GmbH Gesellschaft mbH"),
			shared(sGermany), shared(sYearEnd)},
	}
	write("entities_unsaved_formulas.xlsx", map[string][][]cell{
		"Entities": unsaved,
	}, []string{"Entities"})

	// The same shape, saved properly — but one of the lookups found nothing, so
	// the cell its workbook saved holds the spreadsheet's own error text.
	brokenLookups := [][]cell{
		{shared(sEntity), shared(sLegalName), shared(sCountry), shared(sFinancialYear)},
		{shared(sAcmeHoldingAG), shared(sAcmeHoldingLegal), shared(sSwitzerland), shared(sYearEnd)},
		{shared(sAcmeDE), lookupFailed(`VLOOKUP(A3,Lookups!A:B,2,0)`, "#N/A"), shared(sGermany), shared(sYearEnd)},
	}
	write("entities_broken_lookups.xlsx", map[string][][]cell{
		"Entities": brokenLookups,
	}, []string{"Entities"})

	// A group register written the way a person writes one: the country and the
	// tax residency are the same for the whole sub-group, so the cell is merged
	// down the three rows it covers instead of being typed three times. Read
	// cell by cell, only the first row of each merge holds anything.
	mergedRegister := [][]cell{
		{inline("Entity Name"), inline("Country of incorporation"), inline("Tax residency"), inline("Parent company")},
		{inline("Acme Holding Ltd"), inline("Ireland"), inline("Ireland"), blank()},
		{inline("Acme Trading Ltd"), blank(), blank(), inline("Acme Holding Ltd")},
		{inline("Acme Services Ltd"), blank(), blank(), inline("Acme Holding Ltd")},
		{inline("Acme France SAS"), inline("France"), blank(), inline("Acme Holding Ltd")},
	}
	writeBook("entities_merged_register.xlsx", []sheet{{
		name: "Entities",
		rows: mergedRegister,
		// B2:B4 and C2:C4 run DOWN a column: one value covering three rows,
		// which is what a register means by a merged cell. B5:C5 runs ACROSS
		// two columns that mean different things, and cannot be read the same
		// way without inventing the second value.
		merges: []string{"B2:B4", "C2:C4", "B5:C5"},
	}})

	// The same habit applied ACROSS columns: one heading merged over two
	// columns, so the second column has no heading of its own — and a merged
	// note inside the table, which holds its value in its left-hand cell and
	// leaves the rest of the row empty.
	mergedHeading := [][]cell{
		{inline("Entity Name"), inline("Country of incorporation"), inline("Fiscal calendar"), blank(), inline("Tax residency")},
		{inline("Acme Holding Ltd"), inline("Ireland"), inline("4-4-5"), inline("sunday"), inline("Ireland")},
		{inline("Acme Trading Ltd"), inline("Ireland"), inline("4-4-5"), inline("sunday"), inline("Ireland")},
	}
	writeBook("entities_merged_heading.xlsx", []sheet{{
		name: "Entities",
		rows: mergedHeading,
		// "Fiscal calendar" written across C1:D1, so column D — which holds the
		// day the fiscal week ends on, and decides every period boundary of a
		// 4-4-5 entity — has no heading of its own.
		merges: []string{"C1:D1"},
	}})

	// The workbook somebody keeps working in: last quarter's export is still
	// there on a tab called "Entities", hidden so it is out of the way, while
	// the register being maintained is the visible "Working copy". Both tabs
	// claim the import; only one of them is what anybody means by it.
	hiddenStale := [][]cell{
		{inline("Entity Name"), inline("Country of incorporation")},
		{inline("Old Holding Ltd"), inline("Jersey")},
	}
	visibleCurrent := [][]cell{
		{inline("Entity Name"), inline("Country of incorporation")},
		{inline("Acme Holding Ltd"), inline("Ireland")},
		{inline("Acme Trading Ltd"), inline("Ireland")},
	}
	writeBook("entities_hidden_sheet.xlsx", []sheet{
		{name: "Working copy", rows: visibleCurrent},
		{name: "Entities", rows: hiddenStale, hidden: true},
	})
}

// sheet is one tab of a generated workbook: its rows, the blocks that are
// merged on it, and whether Excel shows the tab at all.
type sheet struct {
	name   string
	rows   [][]cell
	merges []string
	hidden bool
}

// write is writeBook for the fixtures that are plain visible tabs with nothing
// merged on them.
func write(name string, sheets map[string][][]cell, order []string) {
	book := make([]sheet, 0, len(order))
	for _, sheetName := range order {
		book = append(book, sheet{name: sheetName, rows: sheets[sheetName]})
	}
	writeBook(name, book)
}

func writeBook(name string, sheets []sheet) {
	out, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)

	add := func(path, body string) {
		w, err := zw.Create(path)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	}

	var overrides, sheetRefs, rels strings.Builder
	for i, s := range sheets {
		overrides.WriteString(fmt.Sprintf(
			`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1))
		state := ""
		if s.hidden {
			state = ` state="hidden"`
		}
		sheetRefs.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d"%s r:id="rId%d"/>`, s.name, i+1, state, i+1))
		rels.WriteString(fmt.Sprintf(
			`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1))
	}
	styleRel := len(sheets) + 1
	sstRel := len(sheets) + 2
	rels.WriteString(fmt.Sprintf(
		`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, styleRel))
	rels.WriteString(fmt.Sprintf(
		`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>`, sstRel))

	add("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`+
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`+
		`<Default Extension="xml" ContentType="application/xml"/>`+
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`+
		overrides.String()+
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`+
		`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`+
		`</Types>`)

	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`</Relationships>`)

	add("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
		`<sheets>`+sheetRefs.String()+`</sheets></workbook>`)

	add("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		rels.String()+`</Relationships>`)

	// Style 1 is the built-in "mm-dd-yy" date format; style 2 is a custom
	// "yyyy-mm-dd"; style 3 is a plain number format that must NOT be read as a
	// date even though its value is in the serial range.
	add("xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+
		`<numFmts count="1"><numFmt numFmtId="164" formatCode="yyyy\-mm\-dd"/></numFmts>`+
		`<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>`+
		`<fills count="1"><fill><patternFill patternType="none"/></fill></fills>`+
		`<borders count="1"><border/></borders>`+
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`+
		`<cellXfs count="4">`+
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>`+
		`<xf numFmtId="14" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`+
		`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`+
		`<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`+
		`</cellXfs></styleSheet>`)

	var sst strings.Builder
	for _, s := range strings4 {
		sst.WriteString(`<si><t xml:space="preserve">` + escape(s) + `</t></si>`)
	}
	add("xl/sharedStrings.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">%s</sst>`,
		len(strings4), len(strings4), sst.String()))

	for i, s := range sheets {
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s.rows, s.merges))
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	fmt.Println("wrote", name)
}

func sheetXML(rows [][]cell, merges []string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range rows {
		if len(row) == 0 {
			continue // A row with no cells at all is simply absent from the XML.
		}
		sb.WriteString(fmt.Sprintf(`<row r="%d">`, r+1))
		for c, value := range row {
			ref := fmt.Sprintf("%s%d", columnName(c), r+1)
			style := ""
			if value.styleIdx != 0 {
				style = fmt.Sprintf(` s="%d"`, value.styleIdx)
			}
			switch {
			case value.formula != "":
				attrs := style
				if value.cachedType != "" {
					attrs += fmt.Sprintf(` t="%s"`, value.cachedType)
				}
				body := fmt.Sprintf(`<f>%s</f>`, escape(value.formula))
				if value.cached != "" {
					body += fmt.Sprintf(`<v>%s</v>`, escape(value.cached))
				}
				sb.WriteString(fmt.Sprintf(`<c r="%s"%s>%s</c>`, ref, attrs, body))
			case value.shared >= 0:
				sb.WriteString(fmt.Sprintf(`<c r="%s"%s t="s"><v>%d</v></c>`, ref, style, value.shared))
			case value.inline != "":
				sb.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
					ref, style, escape(value.inline)))
			case value.number != "":
				sb.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, style, value.number))
			default:
				sb.WriteString(fmt.Sprintf(`<c r="%s"%s/>`, ref, style))
			}
		}
		sb.WriteString(`</row>`)
	}
	sb.WriteString(`</sheetData>`)
	// A merged block is declared here and nowhere else: the cells it covers
	// simply have no <c> element of their own, exactly as Excel writes them.
	if len(merges) > 0 {
		sb.WriteString(fmt.Sprintf(`<mergeCells count="%d">`, len(merges)))
		for _, ref := range merges {
			sb.WriteString(fmt.Sprintf(`<mergeCell ref="%s"/>`, ref))
		}
		sb.WriteString(`</mergeCells>`)
	}
	sb.WriteString(`</worksheet>`)
	return sb.String()
}

func columnName(i int) string {
	name := ""
	for n := i; ; n = n/26 - 1 {
		name = string(rune('A'+n%26)) + name
		if n < 26 {
			break
		}
	}
	return name
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
