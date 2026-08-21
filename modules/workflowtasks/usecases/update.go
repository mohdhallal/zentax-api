package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

func (uc *UseCases) Update(ctx context.Context, id domain.WorkflowTaskID, input domain.UpdateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	applyDueDateDefaults(&input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)

	wt, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wt == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	return wt, nil
}
