package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
)

func (uc *UseCases) Get(ctx context.Context, id domain.DocumentID) (*domain.DocumentView, error) {
	return uc.view(ctx, id)
}

func (uc *UseCases) List(ctx context.Context, filter domain.ListDocumentsFilter) ([]domain.DocumentView, int, error) {
	return uc.repo.ListViews(ctx, filter)
}

func (uc *UseCases) ListByWorkflow(ctx context.Context, workflowID string) ([]domain.DocumentView, error) {
	exists, err := uc.repo.WorkflowExists(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apperrors.NewNotFound(domain.MsgWorkflowNotFound)
	}
	views, _, err := uc.repo.ListViews(ctx, domain.ListDocumentsFilter{WorkflowID: &workflowID})
	return views, err
}

// ListByTaskInstance lists the documents of one task instance. TaskInstanceRef
// is deliberately NOT read-scoped — it is also the ADR-0018 freeze guard on the
// write paths, and narrowing a guard can only weaken it — so the scope is
// applied by re-asking whether the instance's workflow is readable. Without
// that step an out-of-scope instance would answer 200 with an empty list, which
// still confirms it exists.
func (uc *UseCases) ListByTaskInstance(ctx context.Context, taskInstanceID string) ([]domain.DocumentView, error) {
	ref, err := uc.repo.TaskInstanceRef(ctx, taskInstanceID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, apperrors.NewNotFound(domain.MsgTaskInstanceNotFound)
	}
	readable, err := uc.repo.WorkflowExists(ctx, ref.WorkflowID)
	if err != nil {
		return nil, err
	}
	if !readable {
		return nil, apperrors.NewNotFound(domain.MsgTaskInstanceNotFound)
	}
	views, _, err := uc.repo.ListViews(ctx, domain.ListDocumentsFilter{TaskInstanceID: &taskInstanceID})
	return views, err
}

// ListVersions resolves the document through the read-scoped VIEW rather than
// loadLive, so a document outside the caller's entity subtree is a 404 here
// too — an empty version list would otherwise confirm it exists.
func (uc *UseCases) ListVersions(ctx context.Context, id domain.DocumentID) ([]domain.DocumentVersion, error) {
	if _, err := uc.view(ctx, id); err != nil {
		return nil, err
	}
	return uc.repo.ListVersions(ctx, id)
}
