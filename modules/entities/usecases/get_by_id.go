package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
)

func (uc *UseCases) GetById(ctx context.Context, id domain.EntityID) (*domain.Entity, error) {
	entity, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}
	return entity, nil
}
