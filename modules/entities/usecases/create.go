package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityInput) (*domain.Entity, error) {
	if input.FiscalCalendarPattern == "" {
		input.FiscalCalendarPattern = "standard"
	}
	return uc.repo.Create(ctx, input)
}
