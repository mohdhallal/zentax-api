package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateObligationTypeInput) (*domain.ObligationType, error) {
	if input.Category == "" {
		input.Category = "custom"
	}
	return uc.repo.Create(ctx, input)
}
