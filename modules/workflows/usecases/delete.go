package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowID) error {
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	return nil
}
