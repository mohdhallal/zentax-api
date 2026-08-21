package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowID) error {
	if err := uc.authorizer.EnsureWorkflow(ctx, id, authz.WorkflowWrite); err != nil {
		return err
	}
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	return nil
}
