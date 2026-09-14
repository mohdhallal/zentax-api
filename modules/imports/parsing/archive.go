package parsing

import (
	"archive/zip"
	"bytes"
	"math"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
)

// boundExpansion refuses a workbook by what it would unpack to, BEFORE any of
// it is unpacked.
//
// This is the only bound that arrives in time. The byte cap on the upload and
// the cap on the number of rows are both real, and neither helps here: an .xlsx
// is a zip, deflate compresses a sheet of repeated characters about a thousand
// times, and the memory is spent expanding the parts — which happens in one
// breath, before the first row exists to be counted. A 68 KB upload could fill
// the whole task.
//
// Reading the central directory costs nothing: it is a table of names and
// sizes at the end of the file, and no member is decompressed to read it. The
// declared uncompressed size is a sound upper bound rather than a promise —
// archive/zip stops a member's reader at the size its header declares
// (ErrFormat past it), so a lying header cannot deliver more than it claimed.
func (r *Reader) boundExpansion(data []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return apperrors.NewValidation(errWorkbookUnreadable(err))
	}
	if r.limits.MaxZipParts > 0 && len(archive.File) > r.limits.MaxZipParts {
		return apperrors.NewValidation(errWorkbookTooManyParts(len(archive.File), r.limits.MaxZipParts))
	}

	var unpacked int64
	for _, part := range archive.File {
		size := part.UncompressedSize64
		if size > math.MaxInt64-uint64(unpacked) || int64(size) > r.limits.MaxUnzipBytes-unpacked {
			return apperrors.NewValidation(errWorkbookExpandsTooFar(int64(len(data)), r.limits.MaxUnzipBytes))
		}
		unpacked += int64(size)
	}
	return nil
}
