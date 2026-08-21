package entities_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type EntitiesSuite struct {
	acceptance.Suite
}

func TestEntitiesSuite(t *testing.T) {
	suite.Run(t, new(EntitiesSuite))
}

// TestTenantIsolation drives the full stack — HTTP → RequireTenant → the Tx seam
// that sets app.tenant_id → Postgres RLS — and proves an entity created under one
// tenant is invisible to another across list and get-by-id (ADR-0004). It also
// checks the fail-closed path: no tenant → 401.
func (s *EntitiesSuite) TestTenantIsolation() {
	tenantA := s.InsertTenant("tenant-a", "Tenant A")
	tenantB := s.InsertTenant("tenant-b", "Tenant B")

	// Tenant A creates an entity.
	respA := s.As(tenantA.String()).
		POST(s.T(), "/entities", map[string]any{"name": "Acme A GmbH", "country": "Germany"})
	respA.AssertStatus(s.T(), http.StatusCreated)

	var entityA struct {
		ID string `json:"id"`
	}
	respA.DecodeData(s.T(), &entityA)
	s.Require().NotEmpty(entityA.ID)

	// Tenant B creates its own entity.
	respB := s.As(tenantB.String()).
		POST(s.T(), "/entities", map[string]any{"name": "Beta B SA", "country": "France"})
	respB.AssertStatus(s.T(), http.StatusCreated)

	// Each tenant lists only its own entity.
	var listA, listB []map[string]any
	s.As(tenantA.String()).GET(s.T(), "/entities").DecodeData(s.T(), &listA)
	s.As(tenantB.String()).GET(s.T(), "/entities").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Len(listB, 1)
	s.Require().Equal("Acme A GmbH", listA[0]["name"])
	s.Require().Equal("Beta B SA", listB[0]["name"])

	// Tenant B cannot fetch tenant A's entity by id — RLS yields 404, never another
	// tenant's data.
	s.As(tenantB.String()).
		GET(s.T(), "/entities/"+entityA.ID).
		AssertStatus(s.T(), http.StatusNotFound)

	// A request with no tenant is rejected outright (fail-closed).
	s.Client.External().GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
