package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.EntityObligationID) error {
	if err := uc.authorizer.EnsureEntityObligation(ctx, id, authz.EntityObligationWrite); err != nil {
		return err
	}
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	return uc.audit.Record(ctx, "entity_obligation.deleted", "entity_obligation", id, nil)
}
