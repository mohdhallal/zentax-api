package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	// A task template's scope is its workflow's entity subtree.
	if err := uc.authorizer.EnsureWorkflow(ctx, input.WorkflowID, authz.WorkflowTaskWrite); err != nil {
		return nil, err
	}
	applyDueDateDefaults(&input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)
	return uc.repo.Create(ctx, input)
}
