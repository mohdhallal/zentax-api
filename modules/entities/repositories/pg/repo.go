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
// (ADR-0004), and tenant_id defaults from that GUC on INSERT. Reads carry one
// predicate the database cannot supply: the RBAC read scope (ADR-0012 B-3),
// declared once in sqlConfig.ReadScope so List, GetTotal and GetById all apply
// it.
type EntityRepo struct {
	baserepo.BaseRepo[domain.Entity, domain.EntityID]
}

func NewEntityRepo(db database.ExecerPg) *EntityRepo {
	cfg := sqlConfig
	cfg.ReadScope = readScope(db)
	return &EntityRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.Entity, domain.EntityID](db, cfg),
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
		input.FiscalWeekEndDay,
		input.FiscalYearEndRule,
		input.CustomPeriods,
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
		input.FiscalWeekEndDay,
		input.FiscalYearEndRule,
		input.CustomPeriods,
	)
}
