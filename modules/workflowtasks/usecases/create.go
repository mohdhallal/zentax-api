package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	// A task template's scope is its workflow's entity subtree.
	if err := uc.authorizer.EnsureWorkflow(ctx, input.WorkflowID, authz.WorkflowTaskWrite); err != nil {
		return nil, err
	}
	applyDueDateDefaults(input.TaskType, &input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)
	wt, err := uc.repo.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "workflow_task.created", "workflow_task", wt.ID,
		audit.Changes(nil, auditValues(wt))); err != nil {
		return nil, err
	}
	return wt, nil
}
