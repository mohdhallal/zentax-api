package taskinstances_test

import (
	"net/http"
)

// TestUpdateValidatesAssignee: assigneeId must be an ACTIVE HUMAN member of
// the caller's tenant (users is not RLS-scoped and the column carries no FK,
// so the use case asks the identity module). The full invite → activate →
// assign flow lives in the members suite; this covers the task-side contract.
func (s *TaskInstancesSuite) TestUpdateValidatesAssignee() {
	tenant := s.InsertTenant("as-a", "Assignee Tenant").String()
	other := s.InsertTenant("as-b", "Other Tenant")
	foreign := s.InsertUserWithPassword(other, "foreign@other.test", "correct-horse-battery-staple")

	wfID := s.seedRecurringWorkflow(tenant, "Assign VAT", []string{"M1"})
	s.addTemplate(tenant, wfID, "Prepare", "Tax Manager", "filing_deadline", 5, "before", 0)
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var instances []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	id := instances[0].ID

	// A user of another tenant → 400 with the documented message.
	r := s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
		"status": "in_progress", "assigneeId": foreign.String(),
	})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "assignee is not an active member of this tenant")

	// The caller itself (active human of this tenant) → 200, echoed back.
	var me struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	var updated struct {
		AssigneeID *string `json:"assigneeId"`
	}
	r = s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "in_progress", "assigneeId": me.ID})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Equal(me.ID, *updated.AssigneeID)

	// Omitting assigneeId is still fine (clears it — PUT is a full replacement).
	r = s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "in_progress"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Nil(updated.AssigneeID)
}
