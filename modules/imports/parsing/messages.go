package parsing

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// errUnreadableText refuses a delimited file that cannot be read as a table at
// all. encoding/csv's own message names the line, which is the useful half.
func errUnreadableText(cause error) string {
	return "this file cannot be read as a table: " + cause.Error() +
		". Open it in a spreadsheet and use File → Save As to save it again as CSV."
}

// errQuoteSwallowedLines refuses a delimited file whose quoting ran away. A
// quotation mark that opens a value and never closes it makes every line after
// it part of that one cell, and the file then parses to fewer rows than it
// holds — which is the one outcome an import must never report as clean. The
// refusal names the row the quote opened on, the cell it sits in, and the lines
// it ate, because all three are things the customer can see in their own file.
func errQuoteSwallowedLines(startRow int, cellRef string, firstLost, lastLost, lostCount, others int) string {
	lost := "line " + strconv.Itoa(firstLost)
	if lastLost > firstLost {
		lost = "lines " + strconv.Itoa(firstLost) + "–" + strconv.Itoa(lastLost)
	}
	msg := "row " + strconv.Itoa(startRow) + " has a quotation mark that is never closed, in cell " + cellRef +
		": " + lost + " of this file " + plural(lostCount, "was", "were") + " read as part of that one cell" +
		" rather than as " + plural(lostCount, "a row", "rows") + " of its own, so " +
		strconv.Itoa(lostCount) + " " + plural(lostCount, "row", "rows") +
		" would have been imported as nothing at all. Either wrap that whole value in quotation marks and" +
		" double the quotation mark inside it (\"\"), or remove the quotation mark, then upload the file again."
	if others > 0 {
		msg += " " + strconv.Itoa(others) + " further " + plural(others, "row", "rows") +
			" below it " + plural(others, "does", "do") + " the same thing; fixing this one first will" +
			" show you where."
	}
	return msg
}

// errQuoteInsideAValue records a quotation mark that is inside a value rather
// than around it — an inch mark, a nickname, a height. It is read as content,
// which is right, but a reader that decides that silently is a reader nobody
// can debug.
func errQuoteInsideAValue(line, column int) string {
	return "the quotation mark on line " + strconv.Itoa(line) + ", character " + strconv.Itoa(column) +
		" is inside a value rather than around it, and was read as part of the value." +
		" If it was meant to quote the whole value, wrap the value in quotation marks."
}

// errRowsWiderThanTheHeader refuses rows carrying values past the last column
// heading. Those values have no field to land in, so they would be dropped in
// silence — and the commonest way to produce them is a cell holding several
// values separated by the very character that separates the file's columns.
func errRowsWiderThanTheHeader(format string, headerRow, headerWidth int, wide []wideRow) string {
	headings := strconv.Itoa(headerWidth) + " column " + plural(headerWidth, "heading", "headings") +
		" on row " + strconv.Itoa(headerRow)

	var msg string
	if len(wide) == 1 {
		msg = "row " + strconv.Itoa(wide[0].number) + " carries " + strconv.Itoa(wide[0].width) +
			" values, more than the " + headings + ", so " + quoteValue(wide[0].firstExtraValue) + " in " +
			wide[0].firstExtraRef + " has no column to be read as and would not be imported"
	} else {
		named := make([]string, 0, namedWideRows)
		for i, row := range wide {
			if i == namedWideRows {
				break
			}
			named = append(named, "row "+strconv.Itoa(row.number)+" has "+strconv.Itoa(row.width)+
				" ("+row.firstExtraRef+" is "+quoteValue(row.firstExtraValue)+")")
		}
		msg = strconv.Itoa(len(wide)) + " rows carry more values than the " + headings +
			", so those values have no column to be read as and would not be imported: " +
			strings.Join(named, ", ")
		if len(wide) > namedWideRows {
			msg += " and " + strconv.Itoa(len(wide)-namedWideRows) + " more"
		}
	}
	if format == domain.FormatXLSX {
		return msg + ". Give that column a heading the import knows, or clear the cells to the right of the table."
	}
	return msg + ". A cell that holds several values separated by the same character that separates this file's" +
		" columns has to be wrapped in quotation marks — write \"07-31;01-31\" — otherwise each value after the" +
		" first is read as a column of its own."
}

// namedWideRows is how many over-wide rows a refusal lists by number before it
// counts the rest: enough to see the pattern, not so many that the message
// becomes the problem.
const namedWideRows = 5

// errRowValuesShifted refuses rows that hold more values than the header names
// while still ending inside the table — the shape a split cell leaves when the
// columns at the end of the row are blank.
//
// It has to name a cell, because a count of values is not something anybody can
// find in a spreadsheet. The one it names is the value that ended up furthest
// from home, beside the heading it was actually read under: "12-31" read as a
// payment date is a sentence a tax manager recognises immediately, and 17
// against 16 is not.
func errRowValuesShifted(headerRow, headerWidth int, shifted []shiftedRow) string {
	headings := strconv.Itoa(headerWidth) + " column " + plural(headerWidth, "heading", "headings") +
		" on row " + strconv.Itoa(headerRow)

	var msg string
	if len(shifted) == 1 {
		msg = "row " + strconv.Itoa(shifted[0].number) + " was read as " + strconv.Itoa(shifted[0].width) +
			" values, more than the " + headings + ", so this row's values do not line up with the headings: " +
			quoteValue(shifted[0].lastValue) + " in " + shifted[0].lastRef + " was read as " +
			headingName(shifted[0].heading)
	} else {
		named := make([]string, 0, namedWideRows)
		for i, row := range shifted {
			if i == namedWideRows {
				break
			}
			named = append(named, "row "+strconv.Itoa(row.number)+" was read as "+strconv.Itoa(row.width)+
				" ("+row.lastRef+" is "+quoteValue(row.lastValue)+", read as "+headingName(row.heading)+")")
		}
		msg = strconv.Itoa(len(shifted)) + " rows were read as more values than the " + headings +
			", so their values do not line up with the headings: " + strings.Join(named, ", ")
		if len(shifted) > namedWideRows {
			msg += " and " + strconv.Itoa(len(shifted)-namedWideRows) + " more"
		}
	}
	return msg + ". A cell that holds several values separated by the same character that separates this" +
		" file's columns has to be wrapped in quotation marks — write \"07-31;01-31\" — otherwise each value" +
		" after the first is read as a column of its own and everything after it moves one column to the" +
		" right. If the row instead ends in a separator with nothing after it, delete that separator." +
		" Either way this row would be imported under headings it does not belong to, which is why it is" +
		" refused rather than imported."
}

// headingName is the heading a value was read under, or a plain phrase when
// that column has no heading at all — quoting an empty string back at somebody
// reads as a heading they cannot find.
func headingName(heading string) string {
	if heading == "" {
		return "a column with no heading"
	}
	return "\"" + heading + "\""
}

// errControlCharactersInValues refuses a file whose cells hold characters that
// are not text at all. The commonest cause by far is a UTF-16 file with no
// byte-order mark read as if it were UTF-8 — every value then carries a NUL
// between its characters — so the remedy names that first.
func errControlCharactersInValues(refs []string, total int, example string) string {
	named := strings.Join(refs, ", ")
	if total > len(refs) {
		named += " and " + strconv.Itoa(total-len(refs)) + " more"
	}
	return strconv.Itoa(total) + " " + plural(total, "cell holds", "cells hold") +
		" a character that is not text (" + named + "): " + quoteValue(showControlCharacters(example)) +
		". A file that reads like this is almost always UTF-16 or UTF-32 saved without a byte-order mark." +
		" Open it in a spreadsheet and use File → Save As to save it again as CSV UTF-8, then upload that."
}

// showControlCharacters makes the invisible visible, so the echo in a message
// is something a customer can match against their own cell rather than a value
// that looks exactly right.
func showControlCharacters(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			sb.WriteString("\\u" + fmt.Sprintf("%04X", r))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// errWorkbookExpandsTooFar refuses a workbook by what it would UNPACK to,
// which is the only bound that arrives in time: the cost of a compressed file
// is paid while it is being expanded, long before any row is counted.
func errWorkbookExpandsTooFar(uploaded, limit int64) string {
	return "this workbook is " + formatBytes(uploaded) + " on disk but unpacks to more than " +
		formatBytes(limit) + ", which is more than an import may expand to." +
		" A tax register does not compress that far: if this really is a register that size, export the one" +
		" sheet you want to import as CSV, or split it and import it in parts."
}

// errWorkbookTooManyParts refuses an archive with more members than a workbook
// has, before any of them is opened.
func errWorkbookTooManyParts(parts, limit int) string {
	return "this file is a zip archive with " + strconv.Itoa(parts) +
		" members in it, and a workbook an import can read has at most " + strconv.Itoa(limit) +
		". If you meant to upload a workbook, upload the .xlsx itself rather than an archive of files."
}

// errSheetTooManyCells refuses a sheet whose used range is larger than an
// import may hold in memory at once, naming the sheet and both numbers.
func errSheetTooManyCells(sheet string, cells, limit int) string {
	return "the sheet \"" + sheet + "\" has " + strconv.Itoa(cells) +
		" cells in its used range, and an import may read at most " + strconv.Itoa(limit) +
		". Delete the rows and columns below and to the right of your table — a sheet formatted out to the" +
		" edge of the grid costs memory without holding anything — or import the register in parts."
}

// errUncachedFormulas refuses a workbook whose formulas were saved with no
// result. Such a cell comes back empty and is indistinguishable from a cell the
// customer left blank — and a blank cell in a column the file includes is a
// STATEMENT, which on a second import clears whatever is stored. Reading it as
// empty would therefore not be a missing value; it would be an erasure.
func errUncachedFormulas(sheet string, refs []string, total int) string {
	named := strings.Join(refs, ", ")
	if total > len(refs) {
		named += " and " + strconv.Itoa(total-len(refs)) + " more"
	}
	return "the sheet \"" + sheet + "\" holds " + strconv.Itoa(total) + " " +
		plural(total, "cell", "cells") + " whose formula was saved with no result (" + named +
		"). ZenTax reads the value a workbook saved, not the formula, so those cells look empty and would be" +
		" imported as empty. Open the file in Excel or LibreOffice, save it so the formulas are calculated," +
		" and upload it again — or paste the values in place of the formulas."
}

// namedFormulaCells is how many uncached formula cells a refusal lists by
// reference before it counts the rest.
const namedFormulaCells = 10

// errNotASpreadsheet refuses a file that is not a spreadsheet at all, by what
// it IS. Diagnosing such a file by what it is not — "no header row found" —
// sends somebody to fix column headings in a file that has no columns.
func errNotASpreadsheet(what, remedy string) string {
	return "this is " + what + ", not a spreadsheet ZenTax can read. " + remedy
}

// errOLEContainer refuses the compound-file format that a legacy .xls and a
// password-protected workbook share. The magic cannot tell them apart, so the
// refusal names both and gives both remedies — the old message named only the
// first, and sent anyone with a protected file round a loop that cannot end.
func errOLEContainer() string {
	return "this is either a legacy .xls workbook or a password-protected workbook, and ZenTax cannot read" +
		" either. If it is a legacy .xls, open it and use File → Save As to save it as .xlsx or CSV." +
		" If it is password-protected, open it, remove the password (in Excel: File → Info →" +
		" Protect Workbook → Encrypt with Password, then clear the box), save it, and upload that —" +
		" saving it again as .xlsx keeps the password and produces this same file."
}

// formatBytes writes a size the way the person looking at the file in their
// file manager sees it.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatInt((n+(1<<19))/(1<<20), 10) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt((n+(1<<9))/(1<<10), 10) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " bytes"
	}
}

// quoteValue echoes a cell's content into a message, bounded so that one
// pathological cell cannot make the message unreadable.
func quoteValue(s string) string {
	const maxEcho = 40
	runes := []rune(s)
	if len(runes) > maxEcho {
		s = string(runes[:maxEcho]) + "…"
	}
	return "\"" + s + "\""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// errDelimiterChosen records the separator the file turned out to use, because
// a reader that quietly assumes one is a reader nobody can debug.
func errDelimiterChosen(delimiter rune) string {
	return "read as " + delimiterName(delimiter) + "-separated values."
}

func delimiterName(delimiter rune) string {
	switch delimiter {
	case ';':
		return "semicolon"
	case '\t':
		return "tab"
	case '|':
		return "pipe"
	default:
		return "comma"
	}
}

// errEncodingDecoded records a file that was not UTF-8 but said what it was.
// Only UTF-16 reaches this: its byte pattern is proof rather than a guess — a
// NUL is not a character anybody types — so the file is read and the reading is
// recorded. A single-byte codepage never gets a note like this, because there
// is nothing to write in it that would be true.
func errEncodingDecoded(encoding string) string {
	return "this file is not UTF-8; it was read as " + encoding +
		". If an accented name looks wrong in the preview, re-export it as CSV UTF-8."
}

// errUnknownLegacyEncoding refuses a file written in a single-byte codepage.
//
// This is the refusal that costs a customer one step and saves them a company
// name. The bytes establish only that they are not UTF-8; WHICH codepage they
// are is not written anywhere in the file, and the answer changes every
// accented letter in it. A reader that picks one is not reading the file, it is
// spelling the names itself — and because the name is the natural key this
// import matches on, a name spelled wrong is a record that can never be matched
// to the customer's own register again.
//
// So the message says what the reader can tell, names the cells the bytes are
// in with the bytes themselves made visible, and asks for the one thing that
// settles it: the same file, exported as CSV UTF-8.
func errUnknownLegacyEncoding(invalid int, cells []nonTextCell) string {
	return "this file is not UTF-8, and nothing in it says what it is: " + strconv.Itoa(invalid) + " of its " +
		plural(invalid, "bytes is", "bytes are") + " not valid UTF-8" + namedNonTextCells(invalid, cells) +
		". Those bytes are letters in some legacy codepage, and every codepage spends the same byte on a" +
		" different letter — 0xB3 is \"³\" on a Western European machine and \"ł\" on a Central European one," +
		" 0xE9 is \"é\" in one and \"й\" in Cyrillic. ZenTax cannot tell which country's machine wrote this" +
		" file, and reading it as the wrong one would not fail: it would import names spelled in other" +
		" letters. Open the file in a spreadsheet and use File → Save As → \"CSV UTF-8 (Comma delimited)\"" +
		" (in LibreOffice: Save As, tick \"Edit filter settings\", and set Character set to Unicode (UTF-8))," +
		" then upload that file."
}

// errBytesThatAreNotUTF8 refuses a UTF-8 file that holds a few bytes which are
// not.
//
// A file that is UTF-8 everywhere except in one cell is a concatenated export,
// a value pasted out of an older system, or a file that an editor knowing
// nothing about encodings has touched. The whole-file answer to it — re-read
// everything as a legacy codepage — is the wrong SIZE of answer: it changes
// every accented name in the file, including the ones that were written
// perfectly, to accommodate one cell, and says so in a single remark about the
// file. The evidence is about particular cells, so the refusal is too.
func errBytesThatAreNotUTF8(invalid int, cells []nonTextCell) string {
	return "this file is UTF-8 apart from " + strconv.Itoa(invalid) + " " + plural(invalid, "byte", "bytes") +
		" that " + plural(invalid, "is", "are") + " not" + namedNonTextCells(invalid, cells) +
		". A value holding a byte like that cannot be read as it was written, and reading the whole file in" +
		" some other encoding to accommodate it would change every accented name in the file — so the cells" +
		" are named and refused instead. Retype them, or re-export the file as CSV UTF-8, and upload it again."
}

// namedNonTextCells lists the cells an encoding refusal found the bytes in,
// echoing each one with the offending bytes written out. A cell reference on
// its own is somewhere to look; the value beside it is how the customer knows
// they are looking at the right thing — and seeing "M<0xFC>ller" is what makes
// "this file is in some other encoding" concrete rather than abstract.
func namedNonTextCells(invalid int, cells []nonTextCell) string {
	if len(cells) == 0 {
		return ""
	}
	named := make([]string, 0, len(cells))
	for _, cell := range cells {
		named = append(named, cell.ref+" holds "+quoteValue(cell.show))
	}
	if invalid > len(cells) {
		return " (for example " + strings.Join(named, ", ") + ")"
	}
	return " (" + strings.Join(named, ", ") + ")"
}

// showBytes makes the bytes that are not text visible, so that a cell named in
// an encoding refusal is one the customer can match against their own file:
// everything that decodes is echoed as itself, and a byte that cannot be part
// of any character is written as the byte it actually is.
func showBytes(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			sb.WriteString(fmt.Sprintf("<0x%02X>", s[i]))
			i++
			continue
		}
		sb.WriteString(s[i : i+size])
		i += size
	}
	return sb.String()
}

// errWorkbookUnreadable refuses a workbook the reader cannot open.
func errWorkbookUnreadable(cause error) string {
	return "this workbook cannot be opened: " + cause.Error() +
		". If it is password-protected, remove the password and upload it again;" +
		" otherwise open it and save it again as .xlsx or CSV."
}

// errSheetUnreadable refuses one sheet of an otherwise readable workbook.
func errSheetUnreadable(sheet string, cause error) string {
	return "the sheet \"" + sheet + "\" cannot be read: " + cause.Error() + "."
}
