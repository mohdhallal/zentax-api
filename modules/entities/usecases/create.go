package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityInput) (*domain.Entity, error) {
	// A scoped grant may only create under an entity in its subtree; a root
	// entity (no parent) is tenant-level and needs a tenant-wide grant.
	if err := uc.authorizer.EnsureEntityRef(ctx, input.ParentEntityID, authz.EntityWrite); err != nil {
		return nil, err
	}
	if input.FiscalCalendarPattern == "" {
		input.FiscalCalendarPattern = "standard"
	}
	return uc.repo.Create(ctx, input)
}
