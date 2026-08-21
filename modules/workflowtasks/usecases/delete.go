package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowTaskID) error {
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	return nil
}
