package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	applyDueDateDefaults(&input.DueDateReference, &input.DueDateOffsetUnit, &input.DueDateOffsetDirection)
	return uc.repo.Create(ctx, input)
}
