package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.EntityID, input domain.UpdateEntityInput) (*domain.Entity, error) {
	if err := uc.authorizer.EnsureEntity(ctx, id, authz.EntityWrite); err != nil {
		return nil, err
	}
	if input.FiscalCalendarPattern == "" {
		input.FiscalCalendarPattern = "standard"
	}
	if input.Status == "" {
		input.Status = "active"
	}

	entity, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}
	if err := uc.audit.Record(ctx, "entity.updated", "entity", id, nil); err != nil {
		return nil, err
	}
	return entity, nil
}
