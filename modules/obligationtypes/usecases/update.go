package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
)

func (uc *UseCases) Update(ctx context.Context, id domain.ObligationTypeID, input domain.UpdateObligationTypeInput) (*domain.ObligationType, error) {
	if input.Category == "" {
		input.Category = "custom"
	}
	if input.Status == "" {
		input.Status = "active"
	}

	ot, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if ot == nil {
		return nil, apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}
	return ot, nil
}
