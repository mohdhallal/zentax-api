package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowTaskID) error {
	if err := uc.authorizer.EnsureWorkflowTask(ctx, id, authz.WorkflowTaskWrite); err != nil {
		return err
	}
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	return nil
}
