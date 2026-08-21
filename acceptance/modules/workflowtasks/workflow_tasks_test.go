package workflowtasks_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type WorkflowTasksSuite struct {
	acceptance.Suite
}

func TestWorkflowTasksSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTasksSuite))
}

func (s *WorkflowTasksSuite) createWorkflow(tenant, name string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": name, "workflowCategory": "project", "projectType": "advisory",
	})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

// TestTenantIsolationAndWorkflowFK covers a task template attached to a workflow,
// the JSONB required-documents field, RLS isolation, and cross-tenant workflow FK
// rejection (ADR-0004).
func (s *WorkflowTasksSuite) TestTenantIsolationAndWorkflowFK() {
	tenantA := s.InsertTenant("wt-a", "WT Tenant A").String()
	tenantB := s.InsertTenant("wt-b", "WT Tenant B").String()

	workflowA := s.createWorkflow(tenantA, "Advisory 2025")

	task := map[string]any{
		"workflowId": workflowA, "name": "Prepare docs", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 0,
		"requiredDocuments": []map[string]any{{"name": "VAT ledger", "required": true}},
	}

	// Tenant A adds a task template to its own workflow.
	s.As(tenantA).
		POST(s.T(), "/workflow-tasks", task).
		AssertStatus(s.T(), http.StatusCreated)

	// Each tenant sees only its own task templates (RLS).
	var listA, listB []map[string]any
	s.As(tenantA).GET(s.T(), "/workflow-tasks").DecodeData(s.T(), &listA)
	s.As(tenantB).GET(s.T(), "/workflow-tasks").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Empty(listB)

	// Cross-tenant FK: tenant B references tenant A's workflow — invisible under
	// RLS → FK violation → 400.
	s.As(tenantB).
		POST(s.T(), "/workflow-tasks", task).
		AssertStatus(s.T(), http.StatusBadRequest)

	// No tenant → 401 (fail-closed).
	s.Client.External().GET(s.T(), "/workflow-tasks").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
