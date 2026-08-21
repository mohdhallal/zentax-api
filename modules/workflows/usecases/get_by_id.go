package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
)

func (uc *UseCases) GetById(ctx context.Context, id domain.WorkflowID) (*domain.Workflow, error) {
	wf, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	return wf, nil
}
