package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
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
	if err := uc.audit.Record(ctx, "document.updated", "document", id, map[string]any{
		"documentType": view.DocumentType, "category": view.Category,
	}); err != nil {
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
	return uc.audit.Record(ctx, "document.deleted", "document", id, map[string]any{})
}
