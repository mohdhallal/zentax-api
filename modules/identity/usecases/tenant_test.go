package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

type tenantMocks struct {
	uc      *TenantUseCases
	tenants *domain.TenantRepositoryMock
	audit   *domain.AuditRecorderMock
}

func newTenantUC() tenantMocks {
	m := tenantMocks{tenants: new(domain.TenantRepositoryMock), audit: new(domain.AuditRecorderMock)}
	m.uc = NewTenantUseCases(m.tenants, m.audit)
	return m
}

func (m tenantMocks) assertAll(t *testing.T) {
	t.Helper()
	m.tenants.AssertExpectations(t)
	m.audit.AssertExpectations(t)
}

func TestGetTenant_PinnedToSessionTenant(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.tenants.On("GetByID", ctx, "t1").
		Return(&domain.Tenant{ID: "t1", Slug: "acme", Name: "Acme", Timezone: "UTC"}, nil).Once()

	got, err := m.uc.GetTenant(ctx)
	require.NoError(t, err)
	assert.Equal(t, "t1", got.ID)
	assert.Equal(t, "UTC", got.Timezone)
	m.assertAll(t)
}

func TestGetTenant_NoTenantInContextIsUnauthorized(t *testing.T) {
	m := newTenantUC()
	_, err := m.uc.GetTenant(app.WithRequester(t.Context(), &app.Requester{Kind: app.RequesterUser, ID: "u1"}))
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	m.assertAll(t) // the registry was never touched
}

func TestGetTenant_MissingRowIs404(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.tenants.On("GetByID", ctx, "t1").Return(nil, nil).Once()

	_, err := m.uc.GetTenant(ctx)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	m.assertAll(t)
}

// The update targets the SESSION's tenant — the input carries no id — and
// the audit entry carries the zone only, never the name.
func TestUpdateTenant_PinsSelfTenantAndAuditsZoneOnly(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")
	updated := &domain.Tenant{ID: "t1", Slug: "acme", Name: "Acme Ltd", Timezone: "Europe/London"}
	m.tenants.On("Update", ctx, "t1", "Acme Ltd", "Europe/London").Return(updated, nil).Once()
	m.audit.On("Record", ctx, "tenant.updated", "tenant", "t1",
		mock.MatchedBy(func(d map[string]any) bool {
			_, hasName := d["name"]
			return d["timezone"] == "Europe/London" && !hasName && len(d) == 1
		})).Return(nil).Once()

	got, err := m.uc.UpdateTenant(ctx, domain.UpdateTenantInput{Name: "Acme Ltd", Timezone: "Europe/London"})
	require.NoError(t, err)
	assert.Equal(t, updated, got)
	m.assertAll(t)
}

func TestUpdateTenant_UnknownZoneIs400BeforeAnyWrite(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")

	_, err := m.uc.UpdateTenant(ctx, domain.UpdateTenantInput{Name: "Acme", Timezone: "Mars/Olympus"})
	require.IsType(t, &apperrors.ValidationError{}, err)
	assert.Equal(t, "unknown IANA timezone: Mars/Olympus", err.Error())
	m.assertAll(t) // no Update, no audit
}

func TestUpdateTenant_RejectsLocalBlankAndPadded(t *testing.T) {
	for _, zone := range []string{"Local", "", " ", " UTC", "UTC "} {
		t.Run("["+zone+"]", func(t *testing.T) {
			m := newTenantUC()
			_, err := m.uc.UpdateTenant(adminCtx(t, "t1", "admin-1"), domain.UpdateTenantInput{Name: "Acme", Timezone: zone})
			assert.IsType(t, &apperrors.ValidationError{}, err)
			m.assertAll(t)
		})
	}
}

func TestUpdateTenant_NoTenantInContextIsUnauthorized(t *testing.T) {
	m := newTenantUC()
	_, err := m.uc.UpdateTenant(t.Context(), domain.UpdateTenantInput{Name: "Acme", Timezone: "UTC"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	m.assertAll(t)
}

func TestUpdateTenant_MissingRowIs404(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.tenants.On("Update", ctx, "t1", "Acme", "UTC").Return(nil, nil).Once()

	_, err := m.uc.UpdateTenant(ctx, domain.UpdateTenantInput{Name: "Acme", Timezone: "UTC"})
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	m.assertAll(t) // nothing audited
}

func TestUpdateTenant_AuditFailureFailsTheRequest(t *testing.T) {
	m := newTenantUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.tenants.On("Update", ctx, "t1", "Acme", "UTC").Return(&domain.Tenant{ID: "t1", Timezone: "UTC"}, nil).Once()
	m.audit.On("Record", ctx, "tenant.updated", "tenant", "t1", mock.Anything).Return(assert.AnError).Once()

	_, err := m.uc.UpdateTenant(ctx, domain.UpdateTenantInput{Name: "Acme", Timezone: "UTC"})
	assert.ErrorIs(t, err, assert.AnError)
	m.assertAll(t)
}

// A nil audit recorder is a no-op (unit-test / tooling construction).
func TestUpdateTenant_NilAuditIsNoop(t *testing.T) {
	tenants := new(domain.TenantRepositoryMock)
	uc := NewTenantUseCases(tenants, nil)
	ctx := adminCtx(t, "t1", "admin-1")
	tenants.On("Update", ctx, "t1", "Acme", "UTC").Return(&domain.Tenant{ID: "t1", Timezone: "UTC"}, nil).Once()

	_, err := uc.UpdateTenant(ctx, domain.UpdateTenantInput{Name: "Acme", Timezone: "UTC"})
	require.NoError(t, err)
	tenants.AssertExpectations(t)
}

// ValidateTimezone against the real tz database: the rule the API, the seed
// tool and the unit tests above share.
func TestValidateTimezone_RealZones(t *testing.T) {
	for _, ok := range []string{"UTC", "Europe/London", "Pacific/Kiritimati", "Pacific/Pago_Pago", "America/New_York"} {
		assert.NoError(t, domain.ValidateTimezone(ok, time.LoadLocation), ok)
	}
	for _, bad := range []string{"Mars/Olympus", "Local", "", "Europe/London ", "/etc/passwd", "../zoneinfo/UTC"} {
		err := domain.ValidateTimezone(bad, time.LoadLocation)
		require.Error(t, err, bad)
		assert.IsType(t, &apperrors.ValidationError{}, err)
		assert.Equal(t, domain.MsgUnknownTimezone+bad, err.Error())
	}
}

// Bare abbreviations are refused even though Go can load some of them:
// Postgres treats CET / EST / GMT as fixed offsets (no DST) while browsers
// treat them as zones, so the two sides' "today" would drift (ADR-0023 §6).
func TestUpdateTenant_RejectsAbbreviationsOnlyCanonicalZones(t *testing.T) {
	for _, zone := range []string{"CET", "EST", "GMT", "MET", "EET"} {
		t.Run(zone, func(t *testing.T) {
			m := newTenantUC()
			_, err := m.uc.UpdateTenant(adminCtx(t, "t1", "admin-1"), domain.UpdateTenantInput{Name: "Acme", Timezone: zone})
			require.IsType(t, &apperrors.ValidationError{}, err)
			assert.Equal(t, "unknown IANA timezone: "+zone, err.Error())
			m.assertAll(t)
		})
	}
	// Canonical names (and UTC) still pass validation.
	for _, zone := range []string{"UTC", "Etc/UTC", "Asia/Dubai"} {
		require.NoError(t, domain.ValidateTimezone(zone, time.LoadLocation), zone)
	}
}
