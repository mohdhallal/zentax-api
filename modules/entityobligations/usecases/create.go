package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityObligationInput) (*domain.EntityObligation, error) {
	if err := uc.authorizer.EnsureEntity(ctx, input.EntityID, authz.EntityObligationWrite); err != nil {
		return nil, err
	}
	return uc.repo.Create(ctx, input)
}
