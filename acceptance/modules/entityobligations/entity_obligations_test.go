package entityobligations_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type EntityObligationsSuite struct {
	acceptance.Suite
}

func TestEntityObligationsSuite(t *testing.T) {
	suite.Run(t, new(EntityObligationsSuite))
}

func (s *EntityObligationsSuite) createEntity(tenant, name, country string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.Client.External().WithTenant(tenant).
		POST(s.T(), "/entities", map[string]any{"name": name, "country": country})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

func (s *EntityObligationsSuite) createObligationType(tenant, code string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.Client.External().WithTenant(tenant).
		POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": code, "template": "VAT"})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

// TestTenantIsolationAndCrossTenantFK proves entity obligations are RLS-isolated
// and that the entity_id / obligation_type_id foreign keys are scoped to the
// tenant: tenant B cannot link to tenant A's entity/obligation type because
// those rows are invisible to B, so the FK check fails with 400 (ADR-0004).
func (s *EntityObligationsSuite) TestTenantIsolationAndCrossTenantFK() {
	tenantA := s.InsertTenant("eo-a", "EO Tenant A").String()
	tenantB := s.InsertTenant("eo-b", "EO Tenant B").String()

	entityA := s.createEntity(tenantA, "Acme A", "Germany")
	obTypeA := s.createObligationType(tenantA, "VAT-RET")

	body := map[string]any{
		"entityId":         entityA,
		"obligationTypeId": obTypeA,
		"periodicity":      "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end",
			"offsetUnit": "days", "offsetValue": 20, "offsetDirection": "after",
		},
	}

	// Tenant A links its own entity + obligation type.
	s.Client.External().WithTenant(tenantA).
		POST(s.T(), "/entity-obligations", body).
		AssertStatus(s.T(), http.StatusCreated)

	// Tenant A sees the link; tenant B does not (RLS).
	var listA, listB []map[string]any
	s.Client.External().WithTenant(tenantA).GET(s.T(), "/entity-obligations").DecodeData(s.T(), &listA)
	s.Client.External().WithTenant(tenantB).GET(s.T(), "/entity-obligations").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Empty(listB)

	// Cross-tenant FK: tenant B references tenant A's ids — invisible under RLS,
	// so the FK check fails and the API returns 400, never leaking A's rows.
	s.Client.External().WithTenant(tenantB).
		POST(s.T(), "/entity-obligations", body).
		AssertStatus(s.T(), http.StatusBadRequest)

	// No tenant → 401 (fail-closed).
	s.Client.External().GET(s.T(), "/entity-obligations").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
