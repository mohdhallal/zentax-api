package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Delete removes the obligation — and records what left with it.
//
// Unlike the entity / workflow / workflow-task deletes, this one cascades into
// nothing, so there is no census to take: the evidence worth keeping is the row
// itself, above all the deadline rule that computed the statutory dates of every
// period already filed under it. It is read on the delete's own transaction and
// recorded as the "from" side, because after this commit it exists nowhere else.
func (uc *UseCases) Delete(ctx context.Context, id domain.EntityObligationID) error {
	if err := uc.authorizer.EnsureEntityObligation(ctx, id, authz.EntityObligationWrite); err != nil {
		return err
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return err
	}
	if before == nil {
		return apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}

	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	return uc.audit.Record(ctx, "entity_obligation.deleted", "entity_obligation", id,
		audit.Changes(auditValues(before), nil))
}
