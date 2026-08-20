package obligationtypes_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type ObligationTypesSuite struct {
	acceptance.Suite
}

func TestObligationTypesSuite(t *testing.T) {
	suite.Run(t, new(ObligationTypesSuite))
}

// TestTenantIsolationAndUniqueCode proves obligation types are RLS-isolated per
// tenant and that `code` is unique WITHIN a tenant (409) but reusable ACROSS
// tenants — i.e. the unique constraint is (tenant_id, code), not global.
func (s *ObligationTypesSuite) TestTenantIsolationAndUniqueCode() {
	tenantA := s.InsertTenant("ot-a", "OT Tenant A")
	tenantB := s.InsertTenant("ot-b", "OT Tenant B")

	body := map[string]any{"name": "VAT Return", "code": "VAT-RET", "template": "VAT"}

	// Tenant A creates an obligation type.
	s.Client.External().WithTenant(tenantA.String()).
		POST(s.T(), "/obligation-types", body).
		AssertStatus(s.T(), http.StatusCreated)

	// A duplicate code within tenant A is rejected with 409.
	s.Client.External().WithTenant(tenantA.String()).
		POST(s.T(), "/obligation-types", body).
		AssertStatus(s.T(), http.StatusConflict)

	// The same code under tenant B is allowed — uniqueness is per tenant.
	s.Client.External().WithTenant(tenantB.String()).
		POST(s.T(), "/obligation-types", body).
		AssertStatus(s.T(), http.StatusCreated)

	// Each tenant sees only its own obligation type (RLS).
	var listA, listB []map[string]any
	s.Client.External().WithTenant(tenantA.String()).GET(s.T(), "/obligation-types").DecodeData(s.T(), &listA)
	s.Client.External().WithTenant(tenantB.String()).GET(s.T(), "/obligation-types").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Len(listB, 1)

	// A request without a tenant is rejected (fail-closed).
	s.Client.External().GET(s.T(), "/obligation-types").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
