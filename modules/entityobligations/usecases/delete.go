package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.EntityObligationID) error {
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	return nil
}
