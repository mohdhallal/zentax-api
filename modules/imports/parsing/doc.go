// Package parsing is the reading half of spreadsheet import: it takes the bytes
// a customer uploaded, works out what they are, and hands back the
// format-agnostic sheets modules/imports/domain validates. It knows about zips,
// delimiters and encodings so that nothing else has to.
//
// # What it accepts
//
// The comma-separated format everyone can produce and the workbook format
// everyone actually has: .csv (any of comma, semicolon, tab or pipe, in UTF-8
// or UTF-16) and .xlsx. Everything else is refused by what it IS — a legacy or
// password-protected workbook, a PDF, the HTML table an ERP's "Export to Excel"
// button writes — with what to do about it.
//
// A file in a legacy single-byte codepage is refused too, and that is a
// deliberate line rather than a gap. Which codepage it is is not written
// anywhere in the file, and each one spends the same byte on a different
// letter, so reading one does not fail — it succeeds and gives back a company
// name spelled in other letters. The refusal names the cells the bytes are in
// and asks for the file as CSV UTF-8. See decodeText.
//
// # Being forgiving on purpose
//
// Real exports are not tidy, and refusing them is refusing the customer. So:
// the header row is found rather than assumed (a title, a "generated on" stamp
// and a blank line above it are all normal); blank lines in the middle are
// skipped without shifting any row number; trailing empty rows and columns are
// dropped; a byte-order mark, non-breaking spaces and zero-width characters are
// removed; the encoding is recognised from the bytes rather than taken on trust
// from a mark the file may not carry; a cell merged down a column is read in
// every row it covers, because that is what merging one means; a date cell is
// handed over as an unambiguous ISO date rather than as the serial number
// underneath it or the locale-formatted string on top of it; and a ragged row
// is short, not broken.
//
// Being forgiving about SHAPE is not the same as being forgiving about VALUES.
// Nothing here decides whether a value is acceptable — that is domain's, and it
// is decided against the same rules the single-record routes enforce.
//
// # The one thing that is never forgiven
//
// Reading LESS of a file than it holds, or reading it as something other than
// what it says. A short row is forgiven; a row that holds MORE values than the
// header names is not, whether those values run past the last heading or stop
// inside the table, because either way they are read under headings they do not
// belong to — and a row whose every value is individually valid is a row no
// report further down the line can question. A quotation mark inside a value is
// content; one that opens a value and never closes it is refused by line,
// because encoding/csv would otherwise read the rest of the file into that one
// cell and report a smaller row count than the customer's own with no error at
// all. And nothing that is not text reaches a value: a cell carrying a control
// character is refused by cell, which is where a file in an undeclared
// two-byte encoding ends up if its encoding is somehow not recognised.
// See shape.go.
//
// # Bounds, and when they are measured
//
// A compressed format costs its memory while it is EXPANDED, not while its rows
// are read, so the byte cap on the upload and the cap on the row count both
// arrive too late to bound a workbook: an .xlsx is a zip, and deflate
// compresses a sheet of one repeated character about a thousand times. The
// archive's table of contents is therefore read first and the file refused on
// what it says it unpacks to, before a byte of it is decompressed. See Limits
// and archive.go.
package parsing
