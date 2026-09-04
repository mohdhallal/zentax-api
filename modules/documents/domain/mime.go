package domain

import (
	"mime"
	"net/http"
	"strings"
)

// DefaultContentType is assumed when the client declares none.
const DefaultContentType = "application/octet-stream"

// SniffLen is how many leading bytes the content check inspects
// (http.DetectContentType's window).
const SniffLen = 512

// AllowedContentTypes is the declared-type allowlist (ADR-0022): tax documents,
// spreadsheets, images, archives and the generic binary type.
var AllowedContentTypes = map[string]bool{
	"application/pdf":               true,
	"image/png":                     true,
	"image/jpeg":                    true,
	"image/gif":                     true,
	"text/plain":                    true,
	"text/csv":                      true,
	"application/json":              true,
	"application/xml":               true,
	"text/xml":                      true,
	"application/zip":               true,
	"application/msword":            true,
	"application/vnd.ms-excel":      true,
	"application/vnd.ms-powerpoint": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	DefaultContentType: true,
}

// NormalizeContentType lowercases the declared type and strips parameters
// ("text/plain; charset=utf-8" → "text/plain"); empty → DefaultContentType.
func NormalizeContentType(declared string) string {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return DefaultContentType
	}
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil || mt == "" {
		return strings.ToLower(declared)
	}
	return strings.ToLower(mt)
}

// CheckContentType validates a declared type against the allowlist and
// against the sniffed content of the first bytes (ADR-0001: the server, not the
// client, decides what a file is). Rules:
//   - the declared type must be allowlisted (else 415);
//   - http.DetectContentType on head yields either a GENERIC type
//     (application/octet-stream, application/zip — every OOXML file is a zip —
//     or text/plain in any charset), which accepts whatever was declared, or a
//     CONCRETE type (application/pdf, image/png, …), which must equal the
//     declared one (an XML sniff matches both XML declarations).
//
// Returns the normalized declared type.
func CheckContentType(declared string, head []byte) (string, error) {
	ct := NormalizeContentType(declared)
	if !AllowedContentTypes[ct] {
		return "", &UnsupportedMediaTypeError{Declared: ct}
	}
	sniffed := NormalizeContentType(http.DetectContentType(head))
	if isGenericSniff(sniffed) || sniffed == ct || (isXML(sniffed) && isXML(ct)) {
		return ct, nil
	}
	return "", &UnsupportedMediaTypeError{Declared: ct, Sniffed: sniffed}
}

func isGenericSniff(sniffed string) bool {
	switch sniffed {
	case "application/octet-stream", "application/zip", "text/plain":
		return true
	}
	return false
}

func isXML(ct string) bool {
	return ct == "application/xml" || ct == "text/xml"
}
