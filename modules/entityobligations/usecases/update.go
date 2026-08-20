package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
)

func (uc *UseCases) Update(ctx context.Context, id domain.EntityObligationID, input domain.UpdateEntityObligationInput) (*domain.EntityObligation, error) {
	if input.Status == "" {
		input.Status = "active"
	}

	eo, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if eo == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	return eo, nil
}
