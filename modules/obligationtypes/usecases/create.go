package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
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
	ot, err := uc.repo.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "obligation_type.created", "obligation_type", ot.ID,
		audit.Changes(nil, auditValues(ot))); err != nil {
		return nil, err
	}
	return ot, nil
}
