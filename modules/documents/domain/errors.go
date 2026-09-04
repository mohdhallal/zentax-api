package domain

import "fmt"

func ErrDocumentNotFound(id DocumentID) string {
	return "document not found: " + id
}

func ErrVersionNotFound(id string) string {
	return "document version not found: " + id
}

const (
	MsgWorkflowNotFound        = "workflow not found"
	MsgTaskInstanceNotFound    = "task instance not found"
	MsgTaskInstanceNotInWF     = "task instance does not belong to this workflow"
	MsgApprovedImmutable       = "documents of an approved task are immutable"
	MsgPendingLocked           = "documents of a task awaiting approval are locked until it is approved or rejected"
	MsgInvalidDocumentType     = "invalid document type"
	MsgInvalidCategory         = "invalid category"
	MsgFileNameRequired        = "file name is required"
	MsgFileRequired            = "file is required"
	MsgBlobMissing             = "document blob is missing from storage"
	MsgTaskInstanceApprovedNew = "cannot attach documents to an approved task"
)

// FileTooLargeError → HTTP 413. Carried as its own type so the transport can
// map it without the use case knowing about HTTP.
type FileTooLargeError struct {
	Size int64
	Max  int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("file exceeds the maximum upload size of %d bytes", e.Max)
}

// UnsupportedMediaTypeError → HTTP 415.
type UnsupportedMediaTypeError struct {
	Declared string
	Sniffed  string // "" when the declared type itself is not allowed
}

func (e *UnsupportedMediaTypeError) Error() string {
	if e.Sniffed == "" {
		return "unsupported media type: " + e.Declared
	}
	return fmt.Sprintf("file content (%s) does not match the declared type %s", e.Sniffed, e.Declared)
}
