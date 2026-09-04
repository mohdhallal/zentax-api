package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityInput) (*domain.Entity, error) {
	// A scoped grant may only create under an entity in its subtree; a root
	// entity (no parent) is tenant-level and needs a tenant-wide grant.
	if err := uc.authorizer.EnsureEntityRef(ctx, input.ParentEntityID, authz.EntityWrite); err != nil {
		return nil, err
	}
	applyFiscalDefaults(&input.FiscalCalendarPattern, &input.FiscalWeekEndDay, &input.FiscalYearEndRule)
	if msg := domain.ValidateFiscalConfig(domain.FiscalConfig{
		FiscalCalendarPattern: input.FiscalCalendarPattern,
		FinancialYearEnd:      input.FinancialYearEnd,
		FiscalWeekEndDay:      input.FiscalWeekEndDay,
		FiscalYearEndRule:     input.FiscalYearEndRule,
		CustomPeriods:         input.CustomPeriods,
	}); msg != "" {
		return nil, apperrors.NewValidation(msg)
	}
	e, err := uc.repo.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "entity.created", "entity", e.ID, nil); err != nil {
		return nil, err
	}
	return e, nil
}

// applyFiscalDefaults fills the calendar defaults an omitted field implies
// (standard / Saturday / nearest — the DDL defaults, applied here so the
// repository always writes explicit values).
func applyFiscalDefaults(pattern, weekEndDay, yearEndRule *string) {
	if *pattern == "" {
		*pattern = domain.DefaultFiscalCalendarPattern
	}
	if *weekEndDay == "" {
		*weekEndDay = domain.DefaultFiscalWeekEndDay
	}
	if *yearEndRule == "" {
		*yearEndRule = domain.DefaultFiscalYearEndRule
	}
}
