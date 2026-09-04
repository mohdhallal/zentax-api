package pg

import (
	"context"
	"database/sql"
	"errors"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.TenantRepository = (*TenantRepo)(nil)

// TenantRepo reads and updates the tenant registry row. tenants is NOT
// RLS-scoped (control-plane registry), so every statement carries the id the
// use case pinned from the request context.
type TenantRepo struct {
	db database.ExecerPg
}

func NewTenantRepo(db database.ExecerPg) *TenantRepo {
	return &TenantRepo{db: db}
}

const tenantColumns = `id, slug, name, timezone, created_at, updated_at`

func (r *TenantRepo) GetByID(ctx context.Context, id string) (*domain.Tenant, error) {
	var t domain.Tenant
	err := r.db.GetContext(ctx, &t, `SELECT `+tenantColumns+` FROM tenants WHERE id = $1 LIMIT 1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil means not found
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Update writes name + timezone. Before the write the zone is probed through
// Postgres (`AT TIME ZONE`) so a name Go's tz database knows but this
// Postgres does not (tzdata skew) is refused here, as a 400, instead of
// breaking every report that later evaluates the tenant's "today".
func (r *TenantRepo) Update(ctx context.Context, id, name, timezone string) (*domain.Tenant, error) {
	var probe sql.NullTime
	if err := r.db.GetContext(ctx, &probe, `SELECT NOW() AT TIME ZONE $1`, timezone); err != nil {
		if pgErr := database.IsPgError(err); pgErr != nil && pgErr.Code == "22023" { // invalid_parameter_value
			return nil, apperrors.NewValidation(domain.MsgUnknownTimezone + timezone)
		}
		return nil, err
	}

	var t domain.Tenant
	err := r.db.GetContext(ctx, &t, `
		UPDATE tenants SET name = $2, timezone = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING `+tenantColumns, id, name, timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil means not found
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
