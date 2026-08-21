package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateObligationTypeInput) (*domain.ObligationType, error) {
	// Obligation types are tenant-level content — only a tenant-wide grant.
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.ObligationTypeWrite); err != nil {
		return nil, err
	}
	if input.Category == "" {
		input.Category = "custom"
	}
	return uc.repo.Create(ctx, input)
}
