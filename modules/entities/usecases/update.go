package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update rewrites the entity — and records what the rewrite replaced.
//
// The prior row is read on the same transaction as the write (ADR-0008: the
// evidence and the mutation commit or roll back together), because this is the
// only moment the superseded fiscal calendar exists: no history table stands
// behind entities, so a pattern or year-end an auditor later questions is
// recoverable from the audit envelope alone.
func (uc *UseCases) Update(ctx context.Context, id domain.EntityID, input domain.UpdateEntityInput) (*domain.Entity, error) {
	if err := uc.authorizer.EnsureEntity(ctx, id, authz.EntityWrite); err != nil {
		return nil, err
	}
	applyFiscalDefaults(&input.FiscalCalendarPattern, &input.FiscalWeekEndDay, &input.FiscalYearEndRule)
	if input.Status == "" {
		input.Status = "active"
	}
	if msg := domain.ValidateFiscalConfig(domain.FiscalConfig{
		FiscalCalendarPattern: input.FiscalCalendarPattern,
		FinancialYearEnd:      input.FinancialYearEnd,
		FiscalWeekEndDay:      input.FiscalWeekEndDay,
		FiscalYearEndRule:     input.FiscalYearEndRule,
		CustomPeriods:         input.CustomPeriods,
	}); msg != "" {
		return nil, apperrors.NewValidation(msg)
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}

	entity, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}
	if err := uc.audit.Record(ctx, "entity.updated", "entity", id,
		audit.Changes(auditValues(before), auditValues(entity))); err != nil {
		return nil, err
	}
	return entity, nil
}
