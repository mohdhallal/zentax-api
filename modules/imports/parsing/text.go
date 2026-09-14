package parsing

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// delimiterCandidates are the separators a spreadsheet exports with, in the
// order a tie is broken. A comma is what "CSV" means; a semicolon is what Excel
// writes wherever the decimal separator is a comma, which is most of Europe; a
// tab is what a paste out of a browser or a "Unicode text" export gives; a pipe
// is what a finance system export gives.
var delimiterCandidates = []rune{',', ';', '\t', '|'}

// delimiterSampleLines is how much of a file is read to choose a separator. The
// right one is obvious within a handful of lines and reading more only makes a
// bad file slow to reject.
const delimiterSampleLines = 50

// readDelimited reads a comma-, semicolon-, tab- or pipe-separated file.
func (r *Reader) readDelimited(label string, data []byte) (*domain.File, error) {
	text, encodingNote, flaw := decodeText(data)
	delimiter, delimiterNote := chooseDelimiter(text)

	// The encoding is settled before anything else is read, because it is the
	// one question whose wrong answer is wrong EVERYWHERE: every value in the
	// file is decoded through it, and a file decoded through the wrong codepage
	// comes back readable, bindable, and spelled in different letters. Where the
	// bytes do not settle it, the file is refused with the cells named rather
	// than read through a guess. See decodeText.
	if flaw != nil {
		cells := cellsAtOffsets(text, delimiter, flaw.offsets, namedEncodingCells)
		if flaw.legacy {
			return nil, apperrors.NewValidation(errUnknownLegacyEncoding(flaw.invalid, cells))
		}
		return nil, apperrors.NewValidation(errBytesThatAreNotUTF8(flaw.invalid, cells))
	}

	rows, err := readRecords(text, delimiter)
	if err != nil {
		return nil, err
	}
	// Before anything is trimmed or bounded: prove the parse covered the file.
	// A row count smaller than the customer's own is the one wrong answer this
	// reader must never give quietly.
	quoteNote, err := checkQuoting(text, delimiter, rows)
	if err != nil {
		return nil, err
	}
	rows, err = r.bound(trimEdges(rows))
	if err != nil {
		return nil, err
	}
	// The apostrophe the product's own export writes in front of a value a
	// spreadsheet would evaluate is file syntax, not part of the value, and
	// this is where it comes off — for a delimited file only. See textmarker.go
	// for the contract this is one half of.
	markerNote := stripTextMarkers(rows)

	var notes []domain.Issue
	if encodingNote != "" {
		notes = append(notes, domain.FileWarning(encodingNote))
	}
	if delimiterNote != "" {
		notes = append(notes, domain.FileWarning(delimiterNote))
	}
	if quoteNote != "" {
		notes = append(notes, domain.FileWarning(quoteNote))
	}
	if markerNote != "" {
		notes = append(notes, domain.FileWarning(markerNote))
	}
	return &domain.File{
		Format: domain.FormatCSV,
		Sheets: []domain.Sheet{{Name: label, Format: domain.FormatCSV, Rows: rows}},
		Notes:  notes,
	}, nil
}

// readRecords runs the file through encoding/csv, keeping every row's real line
// number. FieldsPerRecord is off because a real export's rows are ragged, and
// LazyQuotes is on because a real export's quoting is not always closed — both
// are shape, and shape is this package's to forgive.
//
// Forgiving the shape is not the same as forgiving the LOSS a lazy quote can
// cause: under LazyQuotes a value that opens with a quotation mark and never
// closes it eats every line after it. checkQuoting runs over what this function
// produced and proves the parse covered the file; nothing here is trusted to be
// complete on its own.
func readRecords(text string, delimiter rune) ([]domain.Row, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	var rows []domain.Row
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, apperrors.NewValidation(errUnreadableText(err))
		}
		if len(record) == 0 {
			continue
		}
		line, _ := reader.FieldPos(0)
		cells := make([]string, len(record))
		for i, cell := range record {
			cells[i] = domain.CleanCell(cell)
		}
		// How many values the record held is recorded HERE, while it is still
		// known. Trailing empty columns are trimmed later, and after that a row
		// whose cell was split by the file's own separator occupies no more
		// columns than the header does — the split leaves no trace in the shape
		// of the table at all. See checkRowShift.
		rows = append(rows, domain.Row{Number: line, Cells: cells, SourceWidth: len(record)})
	}
	return rows, nil
}

// chooseDelimiter settles which separator holds the file together by reading it
// with each candidate and keeping the one that produces the most consistent
// table. Guessing from a count of characters gets a file with commas inside
// quoted names wrong; parsing does not.
func chooseDelimiter(text string) (delimiter rune, note string) {
	sample := firstLines(text, delimiterSampleLines)

	best, bestScore, bestFields := ',', -1.0, 0
	for _, candidate := range delimiterCandidates {
		fields, score := scoreDelimiter(sample, candidate)
		if fields < 2 {
			continue
		}
		if score > bestScore || (score == bestScore && fields > bestFields) {
			best, bestScore, bestFields = candidate, score, fields
		}
	}
	if bestScore < 0 {
		// Nothing splits it: one column per line, which is a legitimate (if
		// unimportable) file. Comma keeps the rows intact.
		return ',', ""
	}
	if best != ',' {
		return best, errDelimiterChosen(best)
	}
	return best, ""
}

// scoreDelimiter reports the most common column count a candidate produces and
// how much of the sample agrees on it.
func scoreDelimiter(sample string, delimiter rune) (fields int, agreement float64) {
	reader := csv.NewReader(strings.NewReader(sample))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	counts := map[int]int{}
	total := 0
	for {
		record, err := reader.Read()
		if err != nil {
			break
		}
		counts[len(record)]++
		total++
	}
	if total == 0 {
		return 0, 0
	}
	modal, modalCount := 0, 0
	for n, c := range counts {
		if c > modalCount || (c == modalCount && n > modal) {
			modal, modalCount = n, c
		}
	}
	return modal, float64(modalCount) / float64(total)
}

func firstLines(text string, n int) string {
	for i, line := 0, 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		line++
		if line >= n {
			return text[:i]
		}
	}
	return text
}

// decodeText turns the uploaded bytes into text, honouring the byte-order mark
// a spreadsheet's "CSV UTF-8" export leaves behind and the UTF-16 a "Unicode
// text" export produces.
//
// UTF-16 is detected rather than only declared. A byte-order mark is a habit of
// the tool that wrote the file, not a property of the encoding: plenty of
// exports, and every file somebody has trimmed or concatenated, carry none. And
// the failure when it is missing is not a refusal but something worse — a NUL
// is valid UTF-8, so the bytes pass utf8.Valid and every value comes back with
// a stray byte between its characters, a header that still binds, and a report
// that looks readable.
//
// What this reader will NOT do is decide which LEGACY codepage a file is in,
// because the file does not say and the bytes cannot. Every single-byte
// codepage spends the same 128 byte values on a different alphabet: 0xB3 is "³"
// in Windows-1252 and "ł" in Windows-1250, 0xE9 is "é" in one and "й" in
// Windows-1251. Reading a Polish register as Windows-1252 does not fail — it
// succeeds, silently, and hands back a company name spelled in other letters,
// which is then the natural key the import matches on and writes. There is no
// evidence anywhere in such a file for one codepage over another, so this
// reader says what it CAN tell — these bytes are not valid UTF-8, and they are
// in these cells — and refuses. Guessing would not be reading the file; it
// would be inventing a name.
//
// So the third return is not an encoding: it is the evidence that there is no
// answer to give (encodingFlaw). A file with it is refused by readDelimited.
func decodeText(data []byte) (text, note string, flaw *encodingFlaw) {
	switch {
	case bytes.HasPrefix(data, utf8BOM):
		// The mark is a declaration, not a proof. A file two exports were
		// concatenated into carries the first one's mark and the second one's
		// bytes, so what follows it is still measured against UTF-8.
		return checkUTF8(data[len(utf8BOM):])
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], false), errEncodingDecoded("UTF-16 (little-endian)"), nil
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], true), errEncodingDecoded("UTF-16 (big-endian)"), nil
	}
	if bigEndian, ok := looksLikeUTF16(data); ok {
		if bigEndian {
			return decodeUTF16(data, true), errEncodingDecoded("UTF-16 (big-endian, with no byte-order mark)"), nil
		}
		return decodeUTF16(data, false), errEncodingDecoded("UTF-16 (little-endian, with no byte-order mark)"), nil
	}
	return checkUTF8(data)
}

// checkUTF8 reads the bytes as the UTF-8 they are meant to be, and measures
// what is not. The text comes back either way: on the refusing path it is what
// the cells are located in, never what is imported.
func checkUTF8(data []byte) (text, note string, flaw *encodingFlaw) {
	if utf8.Valid(data) {
		return string(data), "", nil
	}
	return string(data), "", scanInvalidUTF8(data)
}

// encodingFlaw is what a file's own bytes prove about its encoding once they
// have failed to be UTF-8: how many of them are not valid UTF-8, where the
// first of those sit, and whether anything in the file is positive evidence of
// UTF-8 at all.
type encodingFlaw struct {
	// legacy is true when the file holds no valid multi-byte UTF-8 character to
	// outweigh its invalid bytes — a file written in a single-byte codepage
	// nothing names. It decides WHICH refusal is given, never whether one is:
	// both are refusals, because neither reading can be proved right.
	legacy bool
	// invalid is how many bytes cannot be part of any UTF-8 character, counted
	// over the whole file so the refusal can say how much of it this is about.
	invalid int
	// offsets are the first of those bytes, as offsets into the decoded text,
	// kept so that the refusal names cells rather than byte positions.
	offsets []int
}

// encodingEvidenceOffsets is how many invalid byte positions are kept to find
// cells from. Several cells is what tells a customer this is their whole file
// rather than one pasted value; a handful of positions is enough to find
// several, and the rest are counted rather than carried.
const encodingEvidenceOffsets = 64

// scanInvalidUTF8 measures a file against UTF-8 twice over: the bytes that
// cannot be part of it, and the multi-byte characters that nothing but UTF-8
// produces.
//
// The second count is what separates the two shapes this reader must tell
// apart. A file whose accented names are written properly and which holds one
// broken byte is a UTF-8 file with damage in one cell; a file with no
// multi-byte character anywhere and high bytes throughout is a legacy codepage.
// Re-reading the first as a codepage would change every name in it to
// accommodate one cell, which is the whole-file answer to a one-cell problem.
func scanInvalidUTF8(data []byte) *encodingFlaw {
	flaw := &encodingFlaw{}
	multiByte := 0
	for i := 0; i < len(data); {
		if data[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			flaw.invalid++
			if len(flaw.offsets) < encodingEvidenceOffsets {
				flaw.offsets = append(flaw.offsets, i)
			}
			i++
			continue
		}
		multiByte++
		i += size
	}
	flaw.legacy = multiByte < flaw.invalid
	return flaw
}

// nonTextCell is one cell named in an encoding refusal: where the customer will
// find it in their own file, and what it holds with the bytes that are not text
// made visible.
type nonTextCell struct {
	ref  string
	show string
}

// namedEncodingCells is how many cells an encoding refusal names before the
// rest are left to the byte count: one is somewhere to look, three prove it is
// the file rather than one stray value.
const namedEncodingCells = 3

// cellsAtOffsets addresses byte offsets the way a customer addresses their own
// file. It parses the text a second time because the offsets are the only
// handle the decoding half has left: by the time rows exist a cell is a Go
// string, and an invalid byte inside a Go string is indistinguishable from a
// REPLACEMENT CHARACTER somebody typed.
//
// Every path that calls it ends in a refusal, so a second pass over the file
// costs nobody anything they were waiting for.
func cellsAtOffsets(text string, delimiter rune, offsets []int, limit int) []nonTextCell {
	if len(offsets) == 0 {
		return nil
	}
	starts := lineStarts(text)
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	var (
		found []nonTextCell
		named = map[string]bool{}
		next  int    // the offset waiting to be placed; they arrive in order.
		open  string // the cell every offset before the next field start is in.
		value string
		begun bool
	)
	// closeField attributes every offset that falls before the next field's
	// first byte to the field that was open, which is the one they sit inside.
	closeField := func(upTo int) {
		for next < len(offsets) && offsets[next] < upTo {
			next++
			if !begun || named[open] {
				continue
			}
			named[open] = true
			found = append(found, nonTextCell{ref: open, show: showBytes(value)})
		}
	}
	for len(found) < limit {
		record, err := reader.Read()
		if err != nil {
			break
		}
		for i, field := range record {
			line, column := reader.FieldPos(i)
			closeField(offsetOfLine(text, starts, line) + column - 1)
			if next >= len(offsets) || len(found) >= limit {
				return found
			}
			open, value, begun = domain.CellRef(i, line), field, true
		}
	}
	closeField(len(text) + 1)
	return found
}

// utf16SampleBytes is how much of a file is measured to recognise UTF-16
// without a byte-order mark. A table's first couple of kilobytes are its header
// and its first rows, which in any tax register are overwhelmingly Latin — and
// that is exactly where the pattern shows.
const utf16SampleBytes = 4096

// looksLikeUTF16 recognises UTF-16 text that does not say so. Every character
// in the Latin range is stored as two bytes, one of which is zero, and which of
// the two is zero is what says little- or big-endian. Text that is genuinely
// UTF-8, ASCII or any single-byte codepage has no NUL bytes at all: a NUL is
// not a character anybody writes in a spreadsheet, which is what makes the
// pattern safe to act on rather than a guess between plausible encodings —
// which is also why UTF-16 is READ where a single-byte codepage is refused.
func looksLikeUTF16(data []byte) (bigEndian, ok bool) {
	size := len(data)
	if size > utf16SampleBytes {
		size = utf16SampleBytes
	}
	pairs := size / 2
	if pairs < utf16MinPairs {
		return false, false
	}

	var lowZero, highZero int
	for i := 0; i+1 < size; i += 2 {
		if data[i] == 0 {
			highZero++ // big-endian: the high byte comes first.
		}
		if data[i+1] == 0 {
			lowZero++ // little-endian: the high byte comes second.
		}
	}
	switch {
	case lowZero*100 >= pairs*utf16MinZeroPercent && highZero*100 <= pairs*utf16MaxOtherPercent:
		return false, true
	case highZero*100 >= pairs*utf16MinZeroPercent && lowZero*100 <= pairs*utf16MaxOtherPercent:
		return true, true
	default:
		return false, false
	}
}

// The proportions looksLikeUTF16 asks for. A file has to be overwhelmingly
// two-byte-with-a-zero in one alignment and almost never in the other, so that
// an ordinary file carrying one stray NUL is not re-read as something else —
// and if the pattern is ever missed, nothing is read as a value regardless:
// checkControlCharacters refuses a cell holding a NUL by name.
const (
	utf16MinPairs        = 8
	utf16MinZeroPercent  = 80
	utf16MaxOtherPercent = 5
)

func decodeUTF16(data []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if bigEndian {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
			continue
		}
		units = append(units, uint16(data[i+1])<<8|uint16(data[i]))
	}
	return string(utf16.Decode(units))
}

// There is deliberately no Windows-1252 decoder here any more. One used to
// stand in for every legacy codepage, and it was wrong in both directions at
// once.
//
// A Polish, Czech, Greek or Cyrillic register decoded through it came back as
// different words entirely \u2014 under a file-level note that named a codepage
// nobody had established, with no cell named and no row refused. And the five
// byte values Windows-1252 does not define decoded to REPLACEMENT CHARACTERS,
// which then sat inside stored entity names: the name is the natural key this
// import matches on, so those were permanent records nobody could match again.
//
// Naming the evidence and refusing is the honest half of what that code did.
// The other half was a guess, and a guess here rewrites a company name.
