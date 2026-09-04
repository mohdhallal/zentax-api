package tenant_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// TenantSuite proves the account-settings surface (ADR-0003 / ADR-0023 §6):
// the tenant's IANA timezone defaults to UTC, rides on /auth/me, is readable
// by every member (GET /tenant), writable by tenant admins only (PUT
// /tenant), validated against the tz database, isolated per tenant and
// audited without the name.
type TenantSuite struct {
	acceptance.Suite
}

func TestTenantSuite(t *testing.T) {
	suite.Run(t, new(TenantSuite))
}

var instant = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

type tenantView struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Timezone  string `json:"timezone"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type meView struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tenant   struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	} `json:"tenant"`
}

func (s *TenantSuite) TestTenantTimezoneSettings() {
	tenant := s.InsertTenant("tz-a", "Zone Tenant A").String()
	other := s.InsertTenant("tz-b", "Zone Tenant B").String()

	// /auth/me carries the tenant with the UTC default — every existing key stays.
	var raw map[string]any
	r := s.AsRole(tenant, "viewer").GET(s.T(), "/auth/me")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &raw)
	for _, key := range []string{"id", "tenantId", "email", "name", "status", "mfaEnabled", "createdAt", "tenant"} {
		s.Require().Contains(raw, key)
	}
	var me meView
	r.DecodeData(s.T(), &me)
	s.Require().Equal(tenant, me.TenantID)
	s.Require().Equal(tenant, me.Tenant.ID)
	s.Require().Equal("tz-a", me.Tenant.Slug)
	s.Require().Equal("Zone Tenant A", me.Tenant.Name)
	s.Require().Equal("UTC", me.Tenant.Timezone)

	// Every role reads the account (member:read); anonymous is rejected.
	var tv tenantView
	r = s.AsRole(tenant, "viewer").GET(s.T(), "/tenant")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &tv)
	s.Require().Equal(tenantView{ID: tenant, Slug: "tz-a", Name: "Zone Tenant A", Timezone: "UTC", CreatedAt: tv.CreatedAt, UpdatedAt: tv.UpdatedAt}, tv)
	s.Require().Regexp(instant, tv.CreatedAt)
	s.Require().Regexp(instant, tv.UpdatedAt)
	s.Client.External().GET(s.T(), "/tenant").AssertStatus(s.T(), http.StatusUnauthorized)

	// Only a tenant admin writes (member:manage).
	body := map[string]any{"name": "Zone Tenant A Ltd", "timezone": "Europe/London"}
	for _, role := range []string{"viewer", "preparer", "reviewer", "manager"} {
		s.AsRole(tenant, role).PUT(s.T(), "/tenant", body).AssertStatus(s.T(), http.StatusForbidden)
	}
	s.AsUngranted(tenant).PUT(s.T(), "/tenant", body).AssertStatus(s.T(), http.StatusForbidden)

	r = s.As(tenant).PUT(s.T(), "/tenant", body)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &tv)
	s.Require().Equal("Zone Tenant A Ltd", tv.Name)
	s.Require().Equal("Europe/London", tv.Timezone)
	s.Require().Equal(tenant, tv.ID)
	s.Require().Equal("tz-a", tv.Slug)

	// /auth/me and GET /tenant reflect it.
	s.AsRole(tenant, "viewer").GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.Require().Equal("Europe/London", me.Tenant.Timezone)
	s.Require().Equal("Zone Tenant A Ltd", me.Tenant.Name)
	s.AsRole(tenant, "preparer").GET(s.T(), "/tenant").DecodeData(s.T(), &tv)
	s.Require().Equal("Europe/London", tv.Timezone)

	// Validation: unknown zone, "Local", blank, over-long, bad name → 400, nothing changes.
	bad := s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant A Ltd", "timezone": "Mars/Olympus"})
	bad.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(bad.BodyString(), "unknown IANA timezone: Mars/Olympus")
	bad = s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant A Ltd", "timezone": "Local"})
	bad.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(bad.BodyString(), "unknown IANA timezone: Local")
	s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant A Ltd", "timezone": ""}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant A Ltd"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "", "timezone": "UTC"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "X", "timezone": "UTC", "slug": "hijack"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/tenant").DecodeData(s.T(), &tv)
	s.Require().Equal("Europe/London", tv.Timezone)
	s.Require().Equal("Zone Tenant A Ltd", tv.Name)

	// The other tenant is untouched — and its admin cannot reach A's row
	// (there is no id to supply: the route is always the caller's tenant).
	s.As(other).GET(s.T(), "/tenant").DecodeData(s.T(), &tv)
	s.Require().Equal(other, tv.ID)
	s.Require().Equal("Zone Tenant B", tv.Name)
	s.Require().Equal("UTC", tv.Timezone)
	var count int
	s.Require().NoError(s.DB.Get(&count, `SELECT COUNT(*) FROM tenants WHERE timezone = 'Europe/London'`))
	s.Require().Equal(1, count)

	// Audit: one tenant.updated on the tenant, carrying the zone only.
	var audit []struct {
		Action       string         `json:"action"`
		ResourceType string         `json:"resourceType"`
		ResourceID   string         `json:"resourceId"`
		Details      map[string]any `json:"details"`
	}
	s.As(tenant).GET(s.T(), "/audit-log?action=tenant.updated").DecodeData(s.T(), &audit)
	s.Require().Len(audit, 1)
	s.Require().Equal("tenant", audit[0].ResourceType)
	s.Require().Equal(tenant, audit[0].ResourceID)
	s.Require().Equal(map[string]any{"timezone": "Europe/London"}, audit[0].Details)
	s.As(other).GET(s.T(), "/audit-log?action=tenant.updated").DecodeData(s.T(), &audit)
	s.Require().Empty(audit)
}

// A tenant seeded with a zone (as cmd/seed-admin --timezone does) reports it
// from the first request.
func (s *TenantSuite) TestSeededTimezone() {
	tenant := s.InsertTenantWithTimezone("tz-c", "Zone Tenant C", "Asia/Singapore").String()
	var me meView
	s.As(tenant).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.Require().Equal("Asia/Singapore", me.Tenant.Timezone)
}
