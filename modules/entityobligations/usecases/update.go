package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.EntityObligationID, input domain.UpdateEntityObligationInput) (*domain.EntityObligation, error) {
	if err := uc.authorizer.EnsureEntityObligation(ctx, id, authz.EntityObligationWrite); err != nil {
		return nil, err
	}
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
	if err := uc.audit.Record(ctx, "entity_obligation.updated", "entity_obligation", id, nil); err != nil {
		return nil, err
	}
	return eo, nil
}
