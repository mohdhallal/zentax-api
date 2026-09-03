package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.EntityObligationRepository = (*EntityObligationRepo)(nil)

// EntityObligationRepo persists entity obligations. RLS (ADR-0004) isolates by
// tenant AND scopes the entity_id / obligation_type_id foreign keys: a
// cross-tenant reference is invisible, so the FK check fails — surfaced as a
// validation error rather than leaking the existence of another tenant's rows.
type EntityObligationRepo struct {
	baserepo.BaseRepo[domain.EntityObligation, domain.EntityObligationID]
}

func NewEntityObligationRepo(db database.ExecerPg) *EntityObligationRepo {
	return &EntityObligationRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.EntityObligation, domain.EntityObligationID](db, sqlConfig),
	}
}

func (r *EntityObligationRepo) Create(
	ctx context.Context, input domain.CreateEntityObligationInput,
) (*domain.EntityObligation, error) {
	eo, err := r.QueryRow(ctx, r.SQL.Create,
		input.EntityID, input.ObligationTypeID, input.TaxReferenceNumber, input.Jurisdiction,
		input.JurisdictionState, input.Currency, input.Periodicity, input.DeadlineRule,
	)
	if err != nil {
		if database.IsForeignKeyViolation(err) {
			return nil, apperrors.NewValidation(domain.ErrEntityObligationRefNotFound())
		}
		return nil, err
	}
	return eo, nil
}

func (r *EntityObligationRepo) Update(
	ctx context.Context, id domain.EntityObligationID, input domain.UpdateEntityObligationInput,
) (*domain.EntityObligation, error) {
	// entity_id / obligation_type_id are fixed at creation, so an update cannot
	// introduce a new FK violation.
	return r.QueryRow(ctx, r.SQL.Update,
		id, input.TaxReferenceNumber, input.Jurisdiction, input.JurisdictionState, input.Currency,
		input.Periodicity, input.DeadlineRule, input.Status,
	)
}
