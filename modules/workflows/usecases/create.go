package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateWorkflowInput) (*domain.Workflow, error) {
	if input.WorkflowCategory == "" {
		input.WorkflowCategory = "recurring"
	}
	return uc.repo.Create(ctx, input)
}
