package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.ObligationTypeRepository = (*ObligationTypeRepo)(nil)

// ObligationTypeRepo persists obligation types. Tenant isolation is enforced by
// RLS (ADR-0004); a duplicate (tenant_id, code) surfaces as a domain conflict.
type ObligationTypeRepo struct {
	baserepo.BaseRepo[domain.ObligationType, domain.ObligationTypeID]
}

func NewObligationTypeRepo(db database.ExecerPg) *ObligationTypeRepo {
	return &ObligationTypeRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.ObligationType, domain.ObligationTypeID](db, sqlConfig),
	}
}

func (r *ObligationTypeRepo) Create(
	ctx context.Context, input domain.CreateObligationTypeInput,
) (*domain.ObligationType, error) {
	ot, err := r.QueryRow(ctx, r.SQL.Create,
		input.Name, input.Code, input.Category, input.Template, input.Description,
	)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return nil, apperrors.NewConflict(domain.ErrObligationTypeCodeExists(input.Code))
		}
		return nil, err
	}
	return ot, nil
}

func (r *ObligationTypeRepo) Update(
	ctx context.Context, id domain.ObligationTypeID, input domain.UpdateObligationTypeInput,
) (*domain.ObligationType, error) {
	ot, err := r.QueryRow(ctx, r.SQL.Update,
		id, input.Name, input.Code, input.Category, input.Template, input.Status, input.Description,
	)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return nil, apperrors.NewConflict(domain.ErrObligationTypeCodeExists(input.Code))
		}
		return nil, err
	}
	return ot, nil
}
