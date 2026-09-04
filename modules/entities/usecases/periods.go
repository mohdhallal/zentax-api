package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Periods lists the entity's reporting periods for a periodicity and fiscal
// year through domain.CalendarFor — the same calendar the generator uses, so
// the period picker and the generated instances can never disagree
// (ADR-0001/0023). The engine's own message is surfaced as a 400.
func (uc *UseCases) Periods(ctx context.Context, id domain.EntityID, periodicity string, financialYear int) ([]deadline.Period, error) {
	entity, err := uc.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	cal, err := domain.CalendarFor(entity, financialYear)
	if err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}
	periods, err := cal.Periods(periodicity)
	if err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}
	return periods, nil
}
