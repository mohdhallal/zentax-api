package domain

import "context"

// DocumentRepository is the persistence port. Every method runs on the request
// transaction (RLS-scoped, ADR-0004); "not found" is a nil result, never an
// error, so use cases decide the HTTP meaning.
type DocumentRepository interface {
	// WorkflowExists reports whether the workflow is visible in the tenant.
	WorkflowExists(ctx context.Context, workflowID string) (bool, error)
	// TaskInstanceRef resolves a task instance's workflow + approval state; nil
	// when it is not in the tenant.
	TaskInstanceRef(ctx context.Context, taskInstanceID string) (*TaskInstanceRef, error)

	// Create inserts the document row and its version 1 (one transaction).
	Create(ctx context.Context, doc NewDocument, ver NewVersion) error
	// GetByID returns the row INCLUDING soft-deleted ones (DeletedAt set).
	GetByID(ctx context.Context, id DocumentID) (*Document, error)
	// BumpVersion increments current_version of a live document and returns
	// the new number; 0 when the document is missing or deleted.
	BumpVersion(ctx context.Context, id DocumentID, label *string) (int, error)
	InsertVersion(ctx context.Context, ver NewVersion) error
	// UpdateMetadata applies the non-nil fields; false = missing/deleted.
	UpdateMetadata(ctx context.Context, id DocumentID, input UpdateDocumentInput) (bool, error)
	// SoftDelete stamps deleted_at on a live document; false = missing/deleted.
	SoftDelete(ctx context.Context, id DocumentID) (bool, error)

	// GetView returns the enriched projection of a LIVE document, nil otherwise.
	GetView(ctx context.Context, id DocumentID) (*DocumentView, error)
	// ListViews lists live documents (latest version each), created_at desc,
	// with the exact total over the same filter.
	ListViews(ctx context.Context, filter ListDocumentsFilter) ([]DocumentView, int, error)
	// ListVersions returns every version of a document, newest first.
	ListVersions(ctx context.Context, documentID DocumentID) ([]DocumentVersion, error)
	// GetVersion returns one version of the document, nil when absent.
	GetVersion(ctx context.Context, documentID DocumentID, versionID string) (*DocumentVersion, error)
	// GetLatestVersion returns the version row matching current_version.
	GetLatestVersion(ctx context.Context, documentID DocumentID) (*DocumentVersion, error)
}

// DocumentUseCases is the application port the handlers drive.
type DocumentUseCases interface {
	Create(ctx context.Context, input CreateDocumentInput) (*DocumentView, error)
	AddVersion(ctx context.Context, id DocumentID, input AddVersionInput) (*DocumentView, error)
	Get(ctx context.Context, id DocumentID) (*DocumentView, error)
	List(ctx context.Context, filter ListDocumentsFilter) ([]DocumentView, int, error)
	ListByWorkflow(ctx context.Context, workflowID string) ([]DocumentView, error)
	ListByTaskInstance(ctx context.Context, taskInstanceID string) ([]DocumentView, error)
	ListVersions(ctx context.Context, id DocumentID) ([]DocumentVersion, error)
	Update(ctx context.Context, id DocumentID, input UpdateDocumentInput) (*DocumentView, error)
	Delete(ctx context.Context, id DocumentID) error
	// Download / DownloadVersion resolve the version and open its blob. The
	// caller must close Download.Body.
	Download(ctx context.Context, id DocumentID) (*Download, error)
	DownloadVersion(ctx context.Context, id DocumentID, versionID string) (*Download, error)
}
