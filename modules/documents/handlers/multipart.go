package handlers

import (
	"errors"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
)

// Error codes / statuses beyond the shared httperr set: the status is carried
// on the AppError directly (httperr.Classify honours it).
const (
	codeFileTooLarge         httperr.AppErrorCode = "FILE_TOO_LARGE"
	codeUnsupportedMediaType httperr.AppErrorCode = "UNSUPPORTED_MEDIA_TYPE"

	// multipartMemory is how much of the form is held in memory before the
	// file part spills to a temp file (removed by net/http after the request).
	multipartMemory = 1 << 20
	// bodySlack is the multipart framing allowance above the file cap.
	bodySlack = 64 * 1024
	// formFieldMax caps a text form field.
	formFieldMax = 5000
)

// upload is the parsed multipart request: the file part + text fields.
type upload struct {
	File   domain.FileUpload
	Fields map[string]string
}

// parseUpload reads a multipart/form-data request: the body is bounded at
// maxBytes + framing slack (→ 413), the "file" part is required, and its
// declared size is checked against the cap before any byte is processed.
// Multipart routes declare no Body schema — the router leaves r.Body alone.
func parseUpload(w http.ResponseWriter, r *http.Request, maxBytes int64, fields ...string) (*upload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, tooLarge(maxBytes)
		}
		if errors.Is(err, http.ErrNotMultipart) {
			return nil, httperr.New(httperr.ErrValidation, "INVALID_BODY", "expected multipart/form-data")
		}
		return nil, httperr.New(httperr.ErrValidation, "INVALID_BODY", "malformed multipart body")
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, httperr.New(httperr.ErrValidation, "INVALID_BODY", "file is required")
		}
		return nil, httperr.New(httperr.ErrValidation, "INVALID_BODY", "malformed file part")
	}
	if hdr.Size > maxBytes {
		_ = file.Close()
		return nil, tooLarge(maxBytes)
	}

	u := &upload{
		File: domain.FileUpload{
			FileName:    hdr.Filename,
			ContentType: hdr.Header.Get("Content-Type"),
			Size:        hdr.Size,
			Body:        file,
		},
		Fields: map[string]string{},
	}
	for _, f := range fields {
		if v, ok := formValue(r.MultipartForm, f); ok {
			if len(v) > formFieldMax {
				_ = file.Close()
				return nil, httperr.New(httperr.ErrValidation, "INVALID_BODY", f+" is too long")
			}
			u.Fields[f] = strings.TrimSpace(v)
		}
	}
	return u, nil
}

func formValue(form *multipart.Form, name string) (string, bool) {
	if form == nil {
		return "", false
	}
	vals, ok := form.Value[name]
	if !ok || len(vals) == 0 {
		return "", false
	}
	return vals[0], true
}

// optional returns nil for an absent field, else a pointer to the (trimmed) value.
func (u *upload) optional(name string) *string {
	v, ok := u.Fields[name]
	if !ok {
		return nil
	}
	return &v
}

func tooLarge(maxBytes int64) *httperr.AppError {
	return &httperr.AppError{
		Code:    codeFileTooLarge,
		Message: (&domain.FileTooLargeError{Max: maxBytes}).Error(),
		Status:  http.StatusRequestEntityTooLarge,
	}
}

// mapUploadError translates the use-case upload errors that have no generic
// HTTP counterpart: 413 for the size cap, 415 for the MIME rules.
func mapUploadError(err error) error {
	var tl *domain.FileTooLargeError
	if errors.As(err, &tl) {
		return &httperr.AppError{Code: codeFileTooLarge, Message: err.Error(), Status: http.StatusRequestEntityTooLarge}
	}
	var umt *domain.UnsupportedMediaTypeError
	if errors.As(err, &umt) {
		return &httperr.AppError{Code: codeUnsupportedMediaType, Message: err.Error(), Status: http.StatusUnsupportedMediaType}
	}
	return err
}
