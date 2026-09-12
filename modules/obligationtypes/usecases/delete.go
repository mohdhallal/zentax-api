package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Delete removes the definition — and records what left with it.
//
// Referencing entity obligations and workflows hold it back with ON DELETE
// RESTRICT, so only an unreferenced definition can reach the delete; the row is
// still read on the delete's own transaction and recorded as the "from" side,
// because after this commit it exists nowhere else.
func (uc *UseCases) Delete(ctx context.Context, id domain.ObligationTypeID) error {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.ObligationTypeWrite); err != nil {
		return err
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return err
	}
	if before == nil {
		return apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}

	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}
	return uc.audit.Record(ctx, "obligation_type.deleted", "obligation_type", id,
		audit.Changes(auditValues(before), nil))
}
