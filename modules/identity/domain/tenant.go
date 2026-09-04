package domain

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
)

// Tenant is the account record as the API exposes it (ADR-0003 / ADR-0023 §6).
// tenants is the control-plane registry — NOT RLS-scoped — so every read and
// write is pinned to the caller's own tenant id from the request context in
// the use case, never to a caller-supplied id.
type Tenant struct {
	ID        string    `db:"id"`
	Slug      string    `db:"slug"`
	Name      string    `db:"name"`
	Timezone  string    `db:"timezone"` // IANA zone name, e.g. "Europe/London"; "UTC" by default
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// DefaultTimezone is the zone a tenant carries until an admin changes it.
const DefaultTimezone = "UTC"

// MaxTimezoneLength mirrors the tenants.timezone column width.
const MaxTimezoneLength = 64

// TenantRepository is the registry persistence. Both methods take the tenant
// id explicitly; the use case supplies app.GetTenantID(ctx).
type TenantRepository interface {
	// GetByID returns the tenant, or nil when no such row exists.
	GetByID(ctx context.Context, id string) (*Tenant, error)
	// Update sets name + timezone and returns the fresh row (nil when no row
	// matched). The zone must already be validated (ValidateTimezone).
	Update(ctx context.Context, id, name, timezone string) (*Tenant, error)
}

// AuditRecorder is the slice of the shared audit trail (platform/audit) the
// tenant use cases append through — kept as a port so unit tests can assert
// the PII-free envelope with a mock.
type AuditRecorder interface {
	Record(ctx context.Context, action, resourceType, resourceID string, details map[string]any) error
}

// TenantUseCases is the account-settings surface: reads for every member
// (member:read), writes for tenant admins (member:manage) — gated at the
// route layer. Both act on the requester's own tenant only.
type TenantUseCases interface {
	GetTenant(ctx context.Context) (*Tenant, error)
	UpdateTenant(ctx context.Context, input UpdateTenantInput) (*Tenant, error)
}

type UpdateTenantInput struct {
	Name     string
	Timezone string
}

// ValidateTimezone accepts a canonical IANA zone name — "UTC" or an
// "Area/Location" name — that Go's tz database can load, and refuses the empty
// string, "Local" (the process zone is never a reference — ADR-0003 §3) and
// bare abbreviations such as "CET" / "EST": Postgres resolves those through its
// fixed-offset abbreviation table (no DST) while browsers and Go treat them as
// zones, so the server's and the client's "today" would drift for half the
// year. The message is the one PUT /tenant returns.
func ValidateTimezone(name string, load func(string) (*time.Location, error)) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed != name || name == "Local" || len(name) > MaxTimezoneLength {
		return apperrors.NewValidation(MsgUnknownTimezone + name)
	}
	if name != "UTC" && !strings.Contains(name, "/") {
		return apperrors.NewValidation(MsgUnknownTimezone + name)
	}
	if _, err := load(name); err != nil {
		return apperrors.NewValidation(MsgUnknownTimezone + name)
	}
	return nil
}
