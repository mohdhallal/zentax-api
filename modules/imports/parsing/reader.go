package parsing

import (
	"bytes"
	"path/filepath"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// Limits bound what one upload may expand to. They are read defences, not
// product limits: a zip that unpacks to gigabytes, or a sheet whose used range
// runs to a million rows because somebody once formatted a column, must cost a
// bounded amount of memory.
//
// The bounds that matter for a workbook are the ones measured BEFORE anything
// is expanded, because a compressed format is paid for while it is being
// expanded and not while its rows are counted. An upload capped at a couple of
// megabytes says nothing about what it unpacks to: deflate compresses a sheet
// of one repeated character about a thousand times.
type Limits struct {
	MaxRows    int
	MaxColumns int
	MaxSheets  int
	// MaxCells bounds one sheet's used range. The row and column caps alone do
	// not: a sheet inside both can still be a million cells.
	MaxCells int
	// MaxUnzipBytes is the total a workbook's parts may declare when unpacked,
	// checked against the archive's own table of contents before a byte is
	// decompressed.
	MaxUnzipBytes int64
	// MaxPartBytes is how large one part may be before it is unpacked to a
	// temporary file instead of being held in memory. It has to be well under
	// MaxUnzipBytes or it never fires, which is what makes the spill path dead
	// code when the two are equal.
	MaxPartBytes int64
	// MaxZipParts bounds the archive's member count, so a zip of a million tiny
	// files is refused from its table of contents.
	MaxZipParts int
}

// DefaultLimits are generous next to any real tax book and small next to any
// machine.
//
// The unzip bound is 16 MiB, chosen against a measurement rather than a guess:
// a workbook at the row cap itself — 1,000 rows of 12 columns, long German
// legal names and all — is 61 KB uploaded and unpacks to 580 KB, so the bound
// leaves a real register at the cap nearly thirty times its own size in
// headroom. It sits eight times over the 2 MiB the handler accepts, and an
// operator who raises that cap should raise this one with it.
//
// What it stops is the other direction: deflate compresses a sheet of one
// repeated character about a thousand to one, so under the library's own
// 128 MiB default a 126 KB upload expanded to 1.3 GiB — more than the whole
// task is given.
func DefaultLimits() Limits {
	return Limits{
		MaxRows:       domain.MaxRows + domain.MaxHeaderScanRows,
		MaxColumns:    256,
		MaxSheets:     32,
		MaxCells:      400_000,
		MaxUnzipBytes: 16 << 20,
		MaxPartBytes:  4 << 20,
		MaxZipParts:   512,
	}
}

// Reader reads uploaded import files. The zero value is not usable; use New.
type Reader struct {
	limits Limits
}

// New is a reader with the default limits.
func New() *Reader { return &Reader{limits: DefaultLimits()} }

// NewWithLimits is a reader with limits of your own.
func NewWithLimits(limits Limits) *Reader { return &Reader{limits: limits} }

// ReadFile sniffs the bytes and reads them into sheets, doing nothing about
// what the values mean. The filename is a hint only — a customer's ".csv" is
// routinely a workbook and the other way round — so what the bytes say wins,
// and the name is used for nothing but the sheet's label.
func (r *Reader) ReadFile(filename string, data []byte) (*domain.File, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, apperrors.NewValidation("the uploaded file is empty.")
	}
	switch {
	case isZipArchive(data):
		return r.readWorkbook(data)
	case isOLEContainer(data):
		return nil, apperrors.NewValidation(errOLEContainer())
	default:
		if foreign, ok := recogniseForeignFormat(data); ok {
			return nil, apperrors.NewValidation(errNotASpreadsheet(foreign.what, foreign.remedy))
		}
		return r.readDelimited(sheetLabel(filename), data)
	}
}

// isZipArchive reports the local-file-header magic every .xlsx starts with.
// (Empty and spanned archives have their own signatures; neither is a workbook
// worth reading, but recognising them keeps the error about the workbook rather
// than about the delimiter.)
func isZipArchive(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' &&
		(data[2] == 0x03 || data[2] == 0x05 || data[2] == 0x07)
}

// isOLEContainer reports the compound-file magic that a legacy .xls, a .doc
// AND a password-protected .xlsx all share — a file-level-encrypted workbook is
// an OLE container with the encrypted package inside it. The magic cannot tell
// the three apart, so the refusal must not claim to (errOLEContainer).
func isOLEContainer(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

// sheetLabel is the name a delimited file's single sheet carries: its own file
// name without the extension, so a workbook and a CSV read the same way when
// the sheet is chosen by name.
func sheetLabel(filename string) string {
	base := filepath.Base(strings.TrimSpace(filename))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// bound applies the reader's structural limits. The row limit is the same
// refusal the validating half makes at Upload.MaxRows (domain.ErrTooManyRows),
// so an oversized file is rejected rather than half-read whichever cap it
// crosses first; the column limit only trims, because a sheet formatted out to
// column ZZ is cost, not content.
func (r *Reader) bound(rows []domain.Row) ([]domain.Row, error) {
	if len(rows) > r.limits.MaxRows {
		return nil, domain.ErrTooManyRows
	}
	for i := range rows {
		if len(rows[i].Cells) > r.limits.MaxColumns {
			rows[i].Cells = rows[i].Cells[:r.limits.MaxColumns]
		}
	}
	return rows, nil
}

// trimEdges drops the trailing empty rows and the trailing empty columns every
// real export carries — the rows below the table somebody once formatted, the
// columns to the right of the last heading.
func trimEdges(rows []domain.Row) []domain.Row {
	last := -1
	width := 0
	for i, row := range rows {
		for col, cell := range row.Cells {
			if cell != "" {
				last = i
				if col+1 > width {
					width = col + 1
				}
			}
		}
	}
	if last < 0 {
		return nil
	}
	rows = rows[:last+1]
	for i := range rows {
		if len(rows[i].Cells) > width {
			rows[i].Cells = rows[i].Cells[:width]
		}
	}
	return rows
}
