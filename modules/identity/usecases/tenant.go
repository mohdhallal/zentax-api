package usecases

import (
	"context"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// TenantUseCases is the account-settings surface (ADR-0003 / ADR-0023 §6).
// The tenant is ALWAYS the requester's own — taken from the request context
// the session bound — never a caller-supplied id: the registry is not
// RLS-scoped, so this pinning is the isolation.
type TenantUseCases struct {
	tenants domain.TenantRepository
	audit   domain.AuditRecorder // nil = no-op
	load    func(string) (*time.Location, error)
}

var _ domain.TenantUseCases = (*TenantUseCases)(nil)

func NewTenantUseCases(tenants domain.TenantRepository, audit domain.AuditRecorder) *TenantUseCases {
	return &TenantUseCases{tenants: tenants, audit: audit, load: time.LoadLocation}
}

func (uc *TenantUseCases) requireTenant(ctx context.Context) (string, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return "", apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	return tenantID, nil
}

// GetTenant returns the caller's tenant.
func (uc *TenantUseCases) GetTenant(ctx context.Context) (*domain.Tenant, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	t, err := uc.tenants.GetByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, apperrors.NewNotFound(domain.MsgTenantNotFound)
	}
	return t, nil
}

// UpdateTenant renames the caller's tenant and sets its IANA timezone. The
// zone is validated with Go's tz database (400 "unknown IANA timezone: …";
// "Local" and blank are refused — ADR-0003 §3).
//
// The audit entry carries the zone on BOTH sides. The account timezone is the
// reference every stored instant is presented in and every reminder is
// scheduled against (ADR-0003), no tenant history table exists, and
// tenants.Update returns the row AFTER the write — so without the prior zone,
// read here, a single entry says only "the zone was set" and an auditor cannot
// tell whether it moved by an hour or by twelve. The name is whitelisted but
// REDACTED: it is free text (ADR-0007/0008), so the trail dates the rename and
// withholds the value, exactly as it does for a member's name — and without it
// a rename-only update would record an empty envelope.
func (uc *TenantUseCases) UpdateTenant(ctx context.Context, input domain.UpdateTenantInput) (*domain.Tenant, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateTimezone(input.Timezone, uc.load); err != nil {
		return nil, err
	}
	// The before state: same transaction, same pinned tenant id, so the two
	// sides of the entry describe one row at two instants.
	before, err := uc.tenants.GetByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.MsgTenantNotFound)
	}
	t, err := uc.tenants.Update(ctx, tenantID, input.Name, input.Timezone)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, apperrors.NewNotFound(domain.MsgTenantNotFound)
	}
	if uc.audit != nil {
		if err := uc.audit.Record(ctx, "tenant.updated", "tenant", t.ID,
			audit.Changes(
				audit.Values{"timezone": before.Timezone, "name": audit.Redact(before.Name)},
				audit.Values{"timezone": t.Timezone, "name": audit.Redact(t.Name)},
			)); err != nil {
			return nil, err
		}
	}
	return t, nil
}
