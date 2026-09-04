package reports_test

import (
	"net/http"
)

// TestAssigneeNameNeverCrossesTenants: users is not RLS-scoped and
// task_instances.assignee_id carries no FK, so the enriched report pins the
// assignee lookup to the row's tenant — a foreign user id yields no name.
func (s *ReportsSuite) TestAssigneeNameNeverCrossesTenants() {
	tenantA := s.InsertTenant("as-a", "Assignee Tenant A")
	tenantB := s.InsertTenant("as-b", "Assignee Tenant B")
	foreignUser := s.InsertUserWithPassword(tenantB, "foreign@b.test", "S3cure-Pass-2026")

	seed := s.seedRecurringWorkflow(tenantA.String(), "Assignee VAT", []string{"M1"})
	s.addTemplate(tenantA.String(), seed.WorkflowID, "Prepare", "filing_deadline", 5, "before", 0, false)
	s.As(tenantA.String()).POST(s.T(), "/workflows/"+seed.WorkflowID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var rows []struct {
		ID           string  `json:"id"`
		AssigneeID   *string `json:"assigneeId"`
		AssigneeName *string `json:"assigneeName"`
	}
	s.As(tenantA.String()).GET(s.T(), "/reports/task-instances?workflowId="+seed.WorkflowID).DecodeData(s.T(), &rows)
	s.Require().Len(rows, 1)

	// A foreign user id is accepted as an opaque uuid but must never resolve to a name.
	s.As(tenantA.String()).PUT(s.T(), "/task-instances/"+rows[0].ID, map[string]any{
		"status": "in_progress", "assigneeId": foreignUser.String(),
	}).AssertStatus(s.T(), http.StatusOK)
	s.As(tenantA.String()).GET(s.T(), "/reports/task-instances?workflowId="+seed.WorkflowID).DecodeData(s.T(), &rows)
	s.Require().Equal(foreignUser.String(), *rows[0].AssigneeID)
	s.Require().Nil(rows[0].AssigneeName)

	// The tenant's own user resolves.
	var me struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	s.As(tenantA.String()).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.As(tenantA.String()).PUT(s.T(), "/task-instances/"+rows[0].ID, map[string]any{
		"status": "in_progress", "assigneeId": me.ID,
	}).AssertStatus(s.T(), http.StatusOK)
	s.As(tenantA.String()).GET(s.T(), "/reports/task-instances?workflowId="+seed.WorkflowID).DecodeData(s.T(), &rows)
	s.Require().NotNil(rows[0].AssigneeName)
	s.Require().Equal(me.Name, *rows[0].AssigneeName)
}
