package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
)

func (uc *UseCases) GetById(ctx context.Context, id domain.EntityObligationID) (*domain.EntityObligation, error) {
	eo, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if eo == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	return eo, nil
}
