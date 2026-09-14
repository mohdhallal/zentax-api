package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

const (
	codeFileTooLarge httperr.AppErrorCode = "FILE_TOO_LARGE"

	// multipartMemory is how much of the form is held in memory before a part
	// spills to a temp file. It is deliberately smaller than the file cap: the
	// content is read into memory immediately afterwards anyway, and letting the
	// framing spill keeps the multipart parser's own allocation independent of
	// how large an operator has set the cap.
	multipartMemory = 1 << 20
	// bodySlack is the multipart framing allowance above the file cap.
	bodySlack = 64 * 1024
	// fileNameMax bounds the one string the customer names the file with. It is
	// stored and returned; it is never used as a path.
	fileNameMax = 200
)

// readUpload reads the "file" part of a multipart/form-data request, BOUNDED
// before anything of it is believed.
//
// The order matters and is the whole of requirement "bound it before anything
// is parsed":
//
//  1. the request body is capped at the file cap plus framing slack, so an
//     oversized upload is refused as it arrives rather than after it has been
//     buffered — a lying Content-Length buys nothing, because the reader itself
//     stops;
//  2. the part's DECLARED size is checked against the cap before a byte of it
//     is copied, which refuses the common case cheaply;
//  3. the copy is bounded again at cap+1, because a declared size is a claim
//     and a chunked body need not declare one at all.
//
// Only then is the content hashed and handed on. The row cap is the reader's,
// applied while the rows are counted out — a row count is not knowable before
// parsing, and stopping at the first row past the cap is the strongest form of
// "nothing beyond the bound is parsed" that a row bound can take.
func readUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) (fileName string, content []byte, checksum string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			// The body outran the cap while it was being read, so how large it
			// actually is was never established: the limit is named, the size
			// is not claimed.
			return "", nil, "", tooLarge(maxBytes, sizeUnknown)
		case errors.Is(err, http.ErrNotMultipart):
			return "", nil, "", validation("send the spreadsheet as multipart/form-data with a \"file\" part")
		default:
			return "", nil, "", validation("this upload's multipart body is malformed")
		}
	}

	part, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return "", nil, "", validation("the upload has no \"file\" part")
		}
		return "", nil, "", validation("this upload's file part is malformed")
	}
	defer func() { _ = part.Close() }()

	if header.Size > maxBytes {
		return "", nil, "", tooLarge(maxBytes, header.Size)
	}

	content, err = io.ReadAll(io.LimitReader(part, maxBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return "", nil, "", tooLarge(maxBytes, sizeUnknown)
		}
		return "", nil, "", validation("this upload's file part could not be read")
	}
	if int64(len(content)) > maxBytes {
		// The part declared nothing (or lied) and the copy stopped one byte
		// past the cap, so all that is known is "more than the cap".
		return "", nil, "", tooLarge(maxBytes, sizeUnknown)
	}
	if len(content) == 0 {
		return "", nil, "", validation("the uploaded file is empty")
	}

	sum := sha256.Sum256(content)
	return safeFileName(header.Filename), content, hex.EncodeToString(sum[:]), nil
}

// safeFileName keeps only the base name and bounds it. The value is stored and
// echoed back, never used to open anything, so this is about what a record may
// hold rather than about traversal — but a name that arrived as a path is not
// the name of a file, and a 4 KB one is not a name at all.
func safeFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	switch name {
	case "", ".", "..", "/":
		return "upload"
	}
	if len(name) > fileNameMax {
		return name[:fileNameMax]
	}
	return name
}

// sizeUnknown is the "size" of a body that outran the cap while it was being
// read: the bound stopped the reader, so nothing established how large the file
// actually is, and a refusal must not claim a number it does not have.
const sizeUnknown int64 = -1

// tooLarge is the byte cap's refusal, and it NAMES THE CAP.
//
// A limit a customer cannot see is a limit they cannot build a file against.
// The number was on this error from the start — in Details, where the browser's
// error reader never looked — so a tax manager with a 4 MB register was told
// only "this file is larger than an import may carry" and had no way to learn
// what would fit. The limit therefore goes in the message, which is the one
// field every client shows, and stays in Details for anything that wants it as
// a number (the wizard's own pre-upload check reads it from the template
// route).
//
// The file's own size is named too WHEN IT IS KNOWN — the part declared it —
// and omitted when the reader stopped early, because "this file is 2 MB" about
// a file whose size was never measured is a guess dressed as a fact.
func tooLarge(maxBytes, fileBytes int64) *httperr.AppError {
	details := map[string]any{"maxBytes": maxBytes}
	message := "this file is larger than an import may carry: the limit is " + formatFileSize(maxBytes) +
		" per file. Split the register and import it in parts."
	if fileBytes > 0 {
		details["fileBytes"] = fileBytes
		message = "this file is " + describeSize(fileBytes, maxBytes) + " and an import may carry up to " +
			describeSize(maxBytes, fileBytes) + " per file. Split the register and import it in parts."
	}
	return &httperr.AppError{
		Code:    codeFileTooLarge,
		Message: message,
		Status:  http.StatusRequestEntityTooLarge,
		Details: details,
	}
}

// tooManyRows is the row cap's refusal, answered with the same status as the
// byte cap's: both say "this file is more than an import may carry", and a
// client that handles one handles the other. It names its cap for the same
// reason tooLarge names its own — and names it in data rows, because that is
// the unit the customer's file is measured in.
func tooManyRows(maxRows int) *httperr.AppError {
	return &httperr.AppError{
		Code: codeFileTooLarge,
		Message: "this file has more rows than an import may carry: the limit is " + formatCount(int64(maxRows)) +
			" data rows per file, not counting the header. Split it and import it in parts — nothing was read past that row.",
		Status:  http.StatusRequestEntityTooLarge,
		Details: map[string]any{"maxRows": maxRows},
	}
}

// describeSize is one size in a sentence that names two of them. Rounded sizes
// collide — 2,097,200 bytes and a 2,097,152-byte cap are both "2 MB" — and
// "this file is 2 MB and an import may carry up to 2 MB" reads as a
// contradiction, so when the two round to the same words both are given exactly
// instead.
func describeSize(bytes, other int64) string {
	if formatFileSize(bytes) == formatFileSize(other) {
		return formatCount(bytes) + " bytes"
	}
	return formatFileSize(bytes)
}

// formatFileSize writes a byte count the way the product writes byte counts
// everywhere a person reads one: 1024-based, one decimal below 10, no trailing
// ".0". It mirrors formatBytes in the web client (client/src/api/imports.ts) —
// the refusal and the sentence on the import page state the same cap, so they
// have to spell it the same way. TestTheRefusalsNameTheirLimit pins the
// shipped defaults' spelling on this side; the client's own suite pins the
// same strings on the other.
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	units := []string{"KB", "MB", "GB"}
	value := float64(bytes) / 1024
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if value < 10 {
		return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0") + " " + units[i]
	}
	return strconv.FormatInt(int64(math.Floor(value+0.5)), 10) + " " + units[i]
}

// formatCount groups thousands, because "1000 data rows" in a refusal is read
// by someone counting rows in a spreadsheet.
func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 {
		return s
	}
	var out strings.Builder
	for i, digit := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}

// mapReadError translates the reading half's bound into an HTTP status that has
// no generic counterpart. Everything else classifies as it stands.
func mapReadError(err error, maxRows int) error {
	if errors.Is(err, domain.ErrTooManyRows) {
		return tooManyRows(maxRows)
	}
	return err
}

func validation(msg string) error {
	return httperr.New(httperr.ErrValidation, msg)
}
