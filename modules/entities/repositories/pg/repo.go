package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.EntityRepository = (*EntityRepo)(nil)

// EntityRepo persists entities. It never filters by tenant_id in SQL — Postgres
// RLS does that transparently via the app.tenant_id GUC bound at the Tx seam
// (ADR-0004), and tenant_id defaults from that GUC on INSERT.
type EntityRepo struct {
	baserepo.BaseRepo[domain.Entity, domain.EntityID]
}

func NewEntityRepo(db database.ExecerPg) *EntityRepo {
	return &EntityRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.Entity, domain.EntityID](db, sqlConfig),
	}
}

func (r *EntityRepo) Create(ctx context.Context, input domain.CreateEntityInput) (*domain.Entity, error) {
	return r.QueryRow(ctx, r.SQL.Create,
		input.ParentEntityID,
		input.Name,
		input.LegalName,
		input.Country,
		input.TaxResidency,
		input.FiscalCalendarPattern,
		input.FinancialYearEnd,
	)
}

func (r *EntityRepo) Update(
	ctx context.Context, id domain.EntityID, input domain.UpdateEntityInput,
) (*domain.Entity, error) {
	return r.QueryRow(ctx, r.SQL.Update,
		id,
		input.ParentEntityID,
		input.Name,
		input.LegalName,
		input.Country,
		input.TaxResidency,
		input.FiscalCalendarPattern,
		input.FinancialYearEnd,
		input.Status,
	)
}
