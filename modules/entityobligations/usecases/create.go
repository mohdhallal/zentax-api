package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateEntityObligationInput) (*domain.EntityObligation, error) {
	if err := uc.authorizer.EnsureEntity(ctx, input.EntityID, authz.EntityObligationWrite); err != nil {
		return nil, err
	}
	eo, err := uc.repo.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "entity_obligation.created", "entity_obligation", eo.ID,
		audit.Changes(nil, auditValues(eo))); err != nil {
		return nil, err
	}
	return eo, nil
}
