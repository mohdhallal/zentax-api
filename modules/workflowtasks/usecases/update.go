package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update rewrites the task template — and records what the rewrite replaced.
//
// The prior row is read on the same transaction as the write (ADR-0008: the
// mutation and its evidence commit or roll back together). This is the only
// place the old template survives: instances already generated keep their own
// copies of due dates, but the template's approval requirement and offsets are
// overwritten in place with no history table behind them.
func (uc *UseCases) Update(ctx context.Context, id domain.WorkflowTaskID, input domain.UpdateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	if err := uc.authorizer.EnsureWorkflowTask(ctx, id, authz.WorkflowTaskWrite); err != nil {
		return nil, err
	}
	applyDueDateDefaults(input.TaskType, &input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}

	wt, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wt == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	if err := uc.audit.Record(ctx, "workflow_task.updated", "workflow_task", id,
		audit.Changes(auditValues(before), auditValues(wt))); err != nil {
		return nil, err
	}
	return wt, nil
}
