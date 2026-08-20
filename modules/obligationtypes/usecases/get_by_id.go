package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
)

func (uc *UseCases) GetById(ctx context.Context, id domain.ObligationTypeID) (*domain.ObligationType, error) {
	ot, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if ot == nil {
		return nil, apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}
	return ot, nil
}
