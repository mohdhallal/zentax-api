package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
)

func (uc *UseCases) Update(ctx context.Context, id domain.WorkflowID, input domain.UpdateWorkflowInput) (*domain.Workflow, error) {
	if input.WorkflowCategory == "" {
		input.WorkflowCategory = "recurring"
	}
	if input.Status == "" {
		input.Status = "draft"
	}

	wf, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	return wf, nil
}
