package authz_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// AuthzSuite drives the full stack — HTTP → RequireSession → Transaction →
// RequireCapability → handler — proving scoped-RBAC capability enforcement
// (ADR-0012): a user's role gates what they may do within their tenant.
type AuthzSuite struct {
	acceptance.Suite
}

func TestAuthzSuite(t *testing.T) {
	suite.Run(t, new(AuthzSuite))
}

// Every role can read (any tenant member may view the compliance program).
func (s *AuthzSuite) TestReadsAllowedForEveryRole() {
	tenant := s.InsertTenant("acme", "Acme").String()
	for _, role := range []string{"viewer", "preparer", "reviewer", "manager", "tenant_admin"} {
		s.AsRole(tenant, role).
			GET(s.T(), "/entities").
			AssertStatus(s.T(), http.StatusOK)
	}
}

// Writes are gated by capability: the capability check runs before body
// validation, so a denied role gets 403 regardless of payload.
func (s *AuthzSuite) TestWritesGatedByRole() {
	tenant := s.InsertTenant("acme", "Acme").String()
	entity := map[string]any{"name": "Acme GmbH", "country": "Germany"}

	// viewer: no writes at all.
	s.AsRole(tenant, "viewer").POST(s.T(), "/entities", entity).
		AssertStatus(s.T(), http.StatusForbidden)

	// preparer: works task instances, but cannot set up entities or workflows.
	s.AsRole(tenant, "preparer").POST(s.T(), "/entities", entity).
		AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "preparer").POST(s.T(), "/workflows", nil).
		AssertStatus(s.T(), http.StatusForbidden)

	// reviewer: approves, but does not set up.
	s.AsRole(tenant, "reviewer").POST(s.T(), "/workflows", nil).
		AssertStatus(s.T(), http.StatusForbidden)

	// manager + tenant_admin hold entity:write.
	s.AsRole(tenant, "manager").POST(s.T(), "/entities", entity).
		AssertStatus(s.T(), http.StatusCreated)
	s.AsRole(tenant, "tenant_admin").POST(s.T(), "/entities", entity).
		AssertStatus(s.T(), http.StatusCreated)
}

// A user with a valid session but no RBAC grant is denied everything — the
// capability check fails closed (an empty grant set holds no capability).
func (s *AuthzSuite) TestUngrantedUserFailsClosed() {
	tenant := s.InsertTenant("acme", "Acme").String()
	s.AsUngranted(tenant).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusForbidden)
	s.AsUngranted(tenant).POST(s.T(), "/entities", map[string]any{"name": "X", "country": "DE"}).
		AssertStatus(s.T(), http.StatusForbidden)
}
