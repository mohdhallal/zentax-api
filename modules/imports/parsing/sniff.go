package parsing

import (
	"bytes"
	"strings"
)

// A file that is not a spreadsheet at all reaches this package regularly: the
// PDF of last year's return, the screenshot of the register, and — by far the
// commonest — the HTML table an ERP's "Export to Excel" button writes and names
// .xls. None of them is dangerous; all of them used to be diagnosed by what
// they are not ("no header row found ... it needs at least \"name\" and
// \"country\" as column headings"), which sends the customer to fix column
// headings in a file that has no columns.
//
// So the bytes are asked what the file IS, and the refusal says so.

// foreignFormat is a format this import cannot read, and what to do instead.
type foreignFormat struct {
	what   string
	remedy string
}

// binarySignatures are the formats that announce themselves in their first
// bytes.
var binarySignatures = []struct {
	magic []byte
	foreignFormat
}{
	{[]byte("%PDF-"), foreignFormat{
		"a PDF",
		"A PDF is a picture of a table, not a table. Export the register from the system that produced it as" +
			" CSV or .xlsx, and upload that.",
	}},
	{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, foreignFormat{
		"a PNG image", "Upload the spreadsheet itself rather than a screenshot of it.",
	}},
	{[]byte{0xFF, 0xD8, 0xFF}, foreignFormat{
		"a JPEG image", "Upload the spreadsheet itself rather than a photograph of it.",
	}},
	{[]byte("GIF8"), foreignFormat{
		"a GIF image", "Upload the spreadsheet itself rather than a picture of it.",
	}},
	{[]byte("{\\rtf"), foreignFormat{
		"a rich-text document",
		"Open it, copy the table into a spreadsheet, and save that as CSV or .xlsx.",
	}},
	{[]byte{0x1F, 0x8B}, foreignFormat{
		"a gzip archive", "Unpack it and upload the spreadsheet inside it.",
	}},
	{[]byte("Rar!"), foreignFormat{
		"a RAR archive", "Unpack it and upload the spreadsheet inside it.",
	}},
	{[]byte("7z\xBC\xAF\x27\x1C"), foreignFormat{
		"a 7-Zip archive", "Unpack it and upload the spreadsheet inside it.",
	}},
}

// markupSniffBytes is how far into a text file the markup sniffer looks. An
// Excel-flavoured HTML export puts its Office namespaces in the first tag, and
// a SpreadsheetML 2003 file its own inside the first few hundred bytes.
const markupSniffBytes = 2048

// utf8BOM is the byte-order mark a spreadsheet's "CSV UTF-8" export leaves on
// the front of a file, skipped before the first character is looked at.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// spreadsheetMLNamespace is what a "XML Spreadsheet 2003" export declares —
// worth telling apart from a web page, because re-saving as .xlsx genuinely
// works for it and does nothing for a web page.
const spreadsheetMLNamespace = "urn:schemas-microsoft-com:office:spreadsheet"

// recogniseForeignFormat names a file that is not a delimited table and not a
// workbook, or reports that the bytes say nothing conclusive — in which case
// the file is read as text, as before.
func recogniseForeignFormat(data []byte) (foreignFormat, bool) {
	for _, signature := range binarySignatures {
		if bytes.HasPrefix(data, signature.magic) {
			return signature.foreignFormat, true
		}
	}
	return recogniseMarkup(data)
}

// recogniseMarkup names the markup formats that arrive wearing a spreadsheet's
// file extension. A leading "<" is the whole test: a delimited file whose first
// character is an angle bracket is not something anyone exports.
func recogniseMarkup(data []byte) (foreignFormat, bool) {
	head := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	if len(head) == 0 || head[0] != '<' {
		return foreignFormat{}, false
	}
	if len(head) > markupSniffBytes {
		head = head[:markupSniffBytes]
	}
	lower := strings.ToLower(string(head))

	if strings.Contains(lower, spreadsheetMLNamespace) {
		return foreignFormat{
			"an XML Spreadsheet 2003 file",
			"Open it in Excel or LibreOffice and use File → Save As to save it as .xlsx or CSV," +
				" then upload that.",
		}, true
	}
	for _, token := range []string{"<html", "<!doctype html", "<table", "<meta", "<body"} {
		if strings.Contains(lower, token) {
			return foreignFormat{
				"a web page saved with a spreadsheet's file extension",
				"An \"Export to Excel\" button that writes an HTML table produces this. Open the file in" +
					" Excel or LibreOffice and use File → Save As to save it as .xlsx or CSV, or export the" +
					" register from your system as CSV instead.",
			}, true
		}
	}
	return foreignFormat{
		"an XML document",
		"Export the register as CSV or .xlsx and upload that.",
	}, true
}
