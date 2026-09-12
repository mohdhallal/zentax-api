package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update rewrites the obligation — and records what the rewrite replaced.
//
// The prior row is read on the same transaction as the write (ADR-0008: the
// mutation and its evidence commit or roll back together). This is the one
// mutation in the domain where the missing before-value was materially unsafe:
// the deadline rule is what computed a statutory filing date, nothing versions
// it, and a PUT replaces it in place. Recording both sides keeps the rule that
// dated an already-filed period reconstructable.
func (uc *UseCases) Update(ctx context.Context, id domain.EntityObligationID, input domain.UpdateEntityObligationInput) (*domain.EntityObligation, error) {
	if err := uc.authorizer.EnsureEntityObligation(ctx, id, authz.EntityObligationWrite); err != nil {
		return nil, err
	}
	if input.Status == "" {
		input.Status = "active"
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}

	eo, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if eo == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityObligationNotFound(id))
	}
	if err := uc.audit.Record(ctx, "entity_obligation.updated", "entity_obligation", id,
		audit.Changes(auditValues(before), auditValues(eo))); err != nil {
		return nil, err
	}
	return eo, nil
}
