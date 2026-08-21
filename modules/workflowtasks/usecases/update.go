package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.WorkflowTaskID, input domain.UpdateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	if err := uc.authorizer.EnsureWorkflowTask(ctx, id, authz.WorkflowTaskWrite); err != nil {
		return nil, err
	}
	applyDueDateDefaults(&input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)

	wt, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wt == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	if err := uc.audit.Record(ctx, "workflow_task.updated", "workflow_task", id, nil); err != nil {
		return nil, err
	}
	return wt, nil
}
