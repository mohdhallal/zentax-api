package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

func (uc *UseCases) GetById(ctx context.Context, id domain.WorkflowTaskID) (*domain.WorkflowTask, error) {
	wt, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if wt == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	return wt, nil
}
