package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Create uploads a new document (version 1) onto a workflow. Scope: the
// workflow's entity subtree (document:write). A task instance, when given,
// must belong to that workflow (400) and must not be approved (409, ADR-0018).
func (uc *UseCases) Create(ctx context.Context, input domain.CreateDocumentInput) (*domain.DocumentView, error) {
	if input.Category == "" {
		input.Category = domain.CategoryCompliance
	}
	if !domain.IsCategory(input.Category) {
		return nil, apperrors.NewValidation(domain.MsgInvalidCategory)
	}
	if !domain.IsDocumentType(input.DocumentType) {
		return nil, apperrors.NewValidation(domain.MsgInvalidDocumentType)
	}
	fileName, err := sanitizeFileName(input.File.FileName)
	if err != nil {
		return nil, err
	}
	if input.File.Body == nil {
		return nil, apperrors.NewValidation(domain.MsgFileRequired)
	}

	if err := uc.authorizer.EnsureWorkflow(ctx, input.WorkflowID, authz.DocumentWrite); err != nil {
		return nil, err
	}
	exists, err := uc.repo.WorkflowExists(ctx, input.WorkflowID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apperrors.NewNotFound(domain.MsgWorkflowNotFound)
	}
	if input.TaskInstanceID != nil {
		ref, err := uc.repo.TaskInstanceRef(ctx, *input.TaskInstanceID)
		if err != nil {
			return nil, err
		}
		if ref == nil || ref.WorkflowID != input.WorkflowID {
			return nil, apperrors.NewValidation(domain.MsgTaskInstanceNotInWF)
		}
		if ref.Locked() {
			return nil, apperrors.NewConflict(ref.LockMessage())
		}
	}

	tenantID, err := tenantFrom(ctx)
	if err != nil {
		return nil, err
	}
	docID, verID := newID(), newID()
	key := StorageKey(tenantID, docID, verID)

	st, err := uc.storeFile(ctx, key, input.File)
	if err != nil {
		return nil, err
	}

	doc := domain.NewDocument{
		ID: docID, WorkflowID: input.WorkflowID, TaskInstanceID: input.TaskInstanceID,
		Category: input.Category, DocumentType: input.DocumentType,
		Label: input.Label, Notes: input.Notes,
	}
	ver := domain.NewVersion{
		ID: verID, DocumentID: docID, Version: 1, StorageKey: key,
		FileName: fileName, FileSize: st.Size, MimeType: st.ContentType, SHA256: st.SHA256,
	}
	if err := uc.repo.Create(ctx, doc, ver); err != nil {
		uc.deleteBlob(ctx, key)
		return nil, err
	}
	if err := uc.audit.Record(ctx, "document.created", "document", docID,
		auditUploadDetails(audit.Changes(nil, auditValues(&domain.Document{
			WorkflowID: doc.WorkflowID, TaskInstanceID: doc.TaskInstanceID,
			Category: doc.Category, DocumentType: doc.DocumentType,
			Label: doc.Label, Notes: doc.Notes,
		})), ver.Version, ver.FileSize, ver.SHA256)); err != nil {
		uc.deleteBlob(ctx, key)
		return nil, err
	}
	return uc.view(ctx, docID)
}

// AddVersion uploads a new version: current_version + 1 on the same
// transaction (the UPDATE row-locks the document, so concurrent uploads
// serialize). Frozen for documents of an approved task instance (409).
func (uc *UseCases) AddVersion(ctx context.Context, id domain.DocumentID, input domain.AddVersionInput) (*domain.DocumentView, error) {
	fileName, err := sanitizeFileName(input.File.FileName)
	if err != nil {
		return nil, err
	}
	if input.File.Body == nil {
		return nil, apperrors.NewValidation(domain.MsgFileRequired)
	}

	doc, err := uc.loadLive(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uc.authorizer.EnsureWorkflow(ctx, doc.WorkflowID, authz.DocumentWrite); err != nil {
		return nil, err
	}
	if err := uc.ensureMutable(ctx, doc); err != nil {
		return nil, err
	}

	version, err := uc.repo.BumpVersion(ctx, id, input.Label)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}

	tenantID, err := tenantFrom(ctx)
	if err != nil {
		return nil, err
	}
	verID := newID()
	key := StorageKey(tenantID, id, verID)

	st, err := uc.storeFile(ctx, key, input.File)
	if err != nil {
		return nil, err
	}
	ver := domain.NewVersion{
		ID: verID, DocumentID: id, Version: version, StorageKey: key,
		FileName: fileName, FileSize: st.Size, MimeType: st.ContentType, SHA256: st.SHA256,
	}
	if err := uc.repo.InsertVersion(ctx, ver); err != nil {
		uc.deleteBlob(ctx, key)
		return nil, err
	}
	if err := uc.audit.Record(ctx, "document.version_added", "document", id,
		auditUploadDetails(audit.Changes(nil, audit.Values{
			"workflowId":     doc.WorkflowID,
			"taskInstanceId": doc.TaskInstanceID,
		}), ver.Version, ver.FileSize, ver.SHA256)); err != nil {
		uc.deleteBlob(ctx, key)
		return nil, err
	}
	return uc.view(ctx, id)
}

// auditUploadDetails attaches the version facts to an upload entry: which
// version this upload IS, how big it was, and the SHA-256 of the bytes that
// were filed. They sit BESIDE the `fields` change set, the way a cascade census
// does, because none of them is a before/after value — an upload is one-sided
// and a version is immutable once written.
//
// The digest is the point of recording any of it. A document entry is evidence
// that a file was filed; without the digest the ledger cannot attest WHICH
// bytes, so a file swapped underneath the same version row would leave the
// trail reading exactly as before. It is a hex digest of file content: no name,
// no free text, no tax figure, and fixed in shape by construction.
func auditUploadDetails(changes map[string]any, version int, fileSize int64, sha256 string) map[string]any {
	if changes == nil {
		changes = map[string]any{}
	}
	changes["version"] = version
	changes["fileSize"] = fileSize
	changes["sha256"] = sha256
	return changes
}
