package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Delete(ctx context.Context, id domain.ObligationTypeID) error {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.ObligationTypeWrite); err != nil {
		return err
	}
	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}
	return uc.audit.Record(ctx, "obligation_type.deleted", "obligation_type", id, nil)
}
