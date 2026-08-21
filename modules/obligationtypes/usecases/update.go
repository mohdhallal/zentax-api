package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.ObligationTypeID, input domain.UpdateObligationTypeInput) (*domain.ObligationType, error) {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.ObligationTypeWrite); err != nil {
		return nil, err
	}
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
	if err := uc.audit.Record(ctx, "obligation_type.updated", "obligation_type", id, nil); err != nil {
		return nil, err
	}
	return ot, nil
}
