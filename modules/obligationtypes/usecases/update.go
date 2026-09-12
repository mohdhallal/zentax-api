package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update rewrites the definition — and records what the rewrite replaced.
//
// The prior row is read on the same transaction as the write (ADR-0008: the
// mutation and its evidence commit or roll back together), which is also what
// lets the 404 be raised before anything is written. Nothing else versions an
// obligation type, so this envelope is the only place a superseded definition
// survives.
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

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}

	ot, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if ot == nil {
		return nil, apperrors.NewNotFound(domain.ErrObligationTypeNotFound(id))
	}
	if err := uc.audit.Record(ctx, "obligation_type.updated", "obligation_type", id,
		audit.Changes(auditValues(before), auditValues(ot))); err != nil {
		return nil, err
	}
	return ot, nil
}
