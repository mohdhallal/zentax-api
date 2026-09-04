// Package domain is the documents model (ADR-0022): a document hangs off a
// workflow (optionally a task instance), is typed by a fixed vocabulary and
// carries an immutable version history whose blobs live behind
// platform/storage. TenantID is infrastructure (RLS) and absent from the model.
package domain

import (
	"io"
	"time"
)

type DocumentID = string

const (
	CategoryCompliance = "compliance"
	CategoryProject    = "project"
)

// DocumentTypes is the vocabulary — verbatim the frontend's
// workflowDocumentTypeValues (shared/schema.ts) and the DB CHECK constraint.
var DocumentTypes = []string{
	"draft_return",
	"final_return",
	"payment_confirmation",
	"advisor_memo",
	"workings",
	"supporting_docs",
	"working_papers",
	"correspondence",
	"deliverable",
	"other",
}

func IsDocumentType(s string) bool {
	for _, t := range DocumentTypes {
		if t == s {
			return true
		}
	}
	return false
}

func IsCategory(s string) bool {
	return s == CategoryCompliance || s == CategoryProject
}

// Document is the documents row.
type Document struct {
	ID             DocumentID `db:"id"`
	WorkflowID     string     `db:"workflow_id"`
	TaskInstanceID *string    `db:"task_instance_id"`
	Category       string     `db:"category"`
	DocumentType   string     `db:"document_type"`
	Label          *string    `db:"label"`
	Notes          *string    `db:"notes"`
	CurrentVersion int        `db:"current_version"`
	DeletedAt      *time.Time `db:"deleted_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	CreatedBy      *string    `db:"created_by"`
	UpdatedBy      *string    `db:"updated_by"`
}

// IsDeleted reports a soft-deleted document (ADR-0007: retained, not visible).
func (d *Document) IsDeleted() bool { return d.DeletedAt != nil }

// DocumentVersion is one immutable upload of a document.
type DocumentVersion struct {
	ID             string    `db:"id"`
	DocumentID     string    `db:"document_id"`
	Version        int       `db:"version"`
	StorageKey     string    `db:"storage_key"`
	FileName       string    `db:"file_name"`
	FileSize       int64     `db:"file_size"`
	MimeType       string    `db:"mime_type"`
	SHA256         string    `db:"sha256"`
	CreatedAt      time.Time `db:"created_at"`
	CreatedBy      *string   `db:"created_by"`
	UploadedByName *string   `db:"uploaded_by_name"` // read-time enrichment (users pinned to the tenant)
}

// DocumentView is the API projection: the document joined to its latest
// version, its workflow / entity context and the uploader's display name.
type DocumentView struct {
	ID             DocumentID `db:"id"`
	WorkflowID     string     `db:"workflow_id"`
	TaskInstanceID *string    `db:"task_instance_id"`
	Category       string     `db:"category"`
	DocumentType   string     `db:"document_type"`
	Label          *string    `db:"label"`
	Notes          *string    `db:"notes"`
	FileName       string     `db:"file_name"`
	FileSize       int64      `db:"file_size"`
	MimeType       string     `db:"mime_type"`
	SHA256         string     `db:"sha256"`
	Version        int        `db:"version"`
	VersionID      string     `db:"version_id"`
	UploadedBy     *string    `db:"uploaded_by"`
	UploadedByName *string    `db:"uploaded_by_name"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	WorkflowName   *string    `db:"workflow_name"`
	EntityID       *string    `db:"entity_id"`
	EntityName     *string    `db:"entity_name"`
	FinancialYear  *string    `db:"financial_year"`
}

// FileUpload is the incoming file as the transport hands it over: the client's
// file name and declared content type, its size, and a one-shot body.
type FileUpload struct {
	FileName    string
	ContentType string // declared by the client; "" → application/octet-stream
	Size        int64
	Body        io.Reader
}

type CreateDocumentInput struct {
	WorkflowID     string
	TaskInstanceID *string
	Category       string // "" → compliance
	DocumentType   string
	Label          *string
	Notes          *string
	File           FileUpload
}

type AddVersionInput struct {
	Label *string // optional relabel alongside the new version
	File  FileUpload
}

// UpdateDocumentInput: nil = unchanged (partial update).
type UpdateDocumentInput struct {
	Label        *string
	Notes        *string
	DocumentType *string
	Category     *string
}

// ListDocumentsFilter: nil pointers mean "no filter". Limit 0 = unbounded
// (the workflow / task-instance sub-lists); otherwise a page.
type ListDocumentsFilter struct {
	EntityID       *string
	WorkflowID     *string
	TaskInstanceID *string
	DocumentType   *string
	Year           *string // workflows.financial_year
	Search         *string // ILIKE over file_name / label / notes
	Limit          int
	Offset         int
}

// NewDocument / NewVersion are the rows a create or version-add persists. Ids
// are minted by the use case because the storage key embeds them.
type NewDocument struct {
	ID             string
	WorkflowID     string
	TaskInstanceID *string
	Category       string
	DocumentType   string
	Label          *string
	Notes          *string
}

type NewVersion struct {
	ID         string
	DocumentID string
	Version    int
	StorageKey string
	FileName   string
	FileSize   int64
	MimeType   string
	SHA256     string
}

// TaskInstanceRef is what the documents module needs to know about a task
// instance: which workflow it belongs to and whether its documents are locked
// (ADR-0018): approved = frozen for good; pending = submitted and awaiting the
// reviewer, so the evidence under review must not change either.
type TaskInstanceRef struct {
	ID         string `db:"id"`
	WorkflowID string `db:"workflow_id"`
	Approved   bool   `db:"approved"`
	Pending    bool   `db:"pending"`
}

// Locked reports whether the instance's documents refuse changes.
func (r *TaskInstanceRef) Locked() bool { return r != nil && (r.Approved || r.Pending) }

// LockMessage is the 409 reason for a locked instance.
func (r *TaskInstanceRef) LockMessage() string {
	if r.Approved {
		return MsgApprovedImmutable
	}
	return MsgPendingLocked
}

// Download is a version ready to stream: metadata + the blob body (caller
// closes) + the content type to serve (the stored MIME type).
type Download struct {
	Version     DocumentVersion
	ContentType string
	Body        io.ReadCloser
}
