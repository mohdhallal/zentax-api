package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update changes metadata (label / notes / documentType / category); a nil
// field is left unchanged, an empty label / notes clears it. Frozen (409) for
// documents of an approved task instance.
func (uc *UseCases) Update(ctx context.Context, id domain.DocumentID, input domain.UpdateDocumentInput) (*domain.DocumentView, error) {
	if input.DocumentType != nil && !domain.IsDocumentType(*input.DocumentType) {
		return nil, apperrors.NewValidation(domain.MsgInvalidDocumentType)
	}
	if input.Category != nil && !domain.IsCategory(*input.Category) {
		return nil, apperrors.NewValidation(domain.MsgInvalidCategory)
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

	updated, err := uc.repo.UpdateMetadata(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}

	view, err := uc.view(ctx, id)
	if err != nil {
		return nil, err
	}
	// `doc` is the row loaded above for the 404 and the ADR-0018 freeze check,
	// on this same transaction, and `view` is the post-state already read for
	// the response — so the before and after of the edit cost no extra query.
	if err := uc.audit.Record(ctx, "document.updated", "document", id,
		audit.Changes(auditValues(doc), auditViewValues(view))); err != nil {
		return nil, err
	}
	return view, nil
}

// Delete soft-deletes (deleted_at): versions and blobs are retained for the
// ADR-0007 purge / legal-hold machinery. A second delete is a 404.
func (uc *UseCases) Delete(ctx context.Context, id domain.DocumentID) error {
	doc, err := uc.loadLive(ctx, id)
	if err != nil {
		return err
	}
	if err := uc.authorizer.EnsureWorkflow(ctx, doc.WorkflowID, authz.DocumentWrite); err != nil {
		return err
	}
	if err := uc.ensureMutable(ctx, doc); err != nil {
		return err
	}
	deleted, err := uc.repo.SoftDelete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}
	// What left, not just that something did. The row survives behind deleted_at
	// for the ADR-0007 purge / legal-hold machinery, but it is invisible to
	// every read path from here on, so the trail records the whitelist as the
	// "from" side — plus how many versions went out of sight with it.
	details := audit.Changes(auditValues(doc), nil)
	if details == nil {
		details = map[string]any{}
	}
	details["versions"] = doc.CurrentVersion
	return uc.audit.Record(ctx, "document.deleted", "document", id, details)
}
