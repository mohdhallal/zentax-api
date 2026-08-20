package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityObligationInput) (*domain.EntityObligation, error) {
	return uc.repo.Create(ctx, input)
}
