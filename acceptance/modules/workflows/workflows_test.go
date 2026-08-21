package workflows_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type WorkflowsSuite struct {
	acceptance.Suite
}

func TestWorkflowsSuite(t *testing.T) {
	suite.Run(t, new(WorkflowsSuite))
}

func (s *WorkflowsSuite) createEntity(tenant, name string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).
		POST(s.T(), "/entities", map[string]any{"name": name, "country": "Germany"})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

func (s *WorkflowsSuite) createObligationType(tenant, code string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).
		POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": code, "template": "VAT"})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

// TestTenantIsolationAndWorkflows covers a recurring workflow (with entity +
// obligation-type refs and a JSONB selected-periods round-trip through the API),
// a project workflow, RLS isolation, and cross-tenant FK rejection (ADR-0004).
func (s *WorkflowsSuite) TestTenantIsolationAndWorkflows() {
	tenantA := s.InsertTenant("wf-a", "WF Tenant A").String()
	tenantB := s.InsertTenant("wf-b", "WF Tenant B").String()

	entityA := s.createEntity(tenantA, "Acme A")
	obTypeA := s.createObligationType(tenantA, "VAT-RET")

	recurring := map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entityA, "obligationTypeId": obTypeA,
		"periodicity": "monthly", "selectedPeriods": []string{"M1", "M2"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	}

	// Tenant A creates a recurring workflow; the JSONB period list round-trips.
	var created struct {
		ID              string   `json:"id"`
		SelectedPeriods []string `json:"selectedPeriods"`
	}
	resp := s.As(tenantA).POST(s.T(), "/workflows", recurring)
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &created)
	s.Require().Equal([]string{"M1", "M2"}, created.SelectedPeriods)

	// Tenant B creates a project workflow (no entity / obligation type needed).
	project := map[string]any{
		"name": "Audit 2025", "workflowCategory": "project", "projectType": "audit_verification",
	}
	s.As(tenantB).
		POST(s.T(), "/workflows", project).
		AssertStatus(s.T(), http.StatusCreated)

	// Each tenant sees only its own workflow (RLS).
	var listA, listB []map[string]any
	s.As(tenantA).GET(s.T(), "/workflows").DecodeData(s.T(), &listA)
	s.As(tenantB).GET(s.T(), "/workflows").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Len(listB, 1)

	// Cross-tenant FK: tenant B references tenant A's entity — invisible under RLS
	// → FK violation → 400.
	s.As(tenantB).
		POST(s.T(), "/workflows", recurring).
		AssertStatus(s.T(), http.StatusBadRequest)

	// No tenant → 401 (fail-closed).
	s.Client.External().GET(s.T(), "/workflows").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
