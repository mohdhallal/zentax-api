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

func (uc *UseCases) ListByTaskInstance(ctx context.Context, taskInstanceID string) ([]domain.DocumentView, error) {
	ref, err := uc.repo.TaskInstanceRef(ctx, taskInstanceID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, apperrors.NewNotFound(domain.MsgTaskInstanceNotFound)
	}
	views, _, err := uc.repo.ListViews(ctx, domain.ListDocumentsFilter{TaskInstanceID: &taskInstanceID})
	return views, err
}

func (uc *UseCases) ListVersions(ctx context.Context, id domain.DocumentID) ([]domain.DocumentVersion, error) {
	if _, err := uc.loadLive(ctx, id); err != nil {
		return nil, err
	}
	return uc.repo.ListVersions(ctx, id)
}
