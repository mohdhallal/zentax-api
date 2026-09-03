package taskinstances_test

import (
	"net/http"
)

// TestUpdateOverridesDueDateAndStampsCompletion: PUT is a full replacement of
// the editable subset — dueDate is optional (omit = keep), a completed status
// stamps completed_at exactly once, and leaving completed clears it.
func (s *TaskInstancesSuite) TestUpdateOverridesDueDateAndStampsCompletion() {
	tenant := s.InsertTenant("up-a", "Update Tenant").String()
	wfID := s.seedRecurringWorkflow(tenant, "Update VAT", []string{"M1"})
	s.addTemplate(tenant, wfID, "Prepare", "Tax Manager", "filing_deadline", 5, "before", 0)
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var instances []struct {
		ID          string  `json:"id"`
		DueDate     string  `json:"dueDate"`
		CompletedAt *string `json:"completedAt"`
		Notes       *string `json:"notes"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	id := instances[0].ID
	s.Require().Equal("2025-02-10", instances[0].DueDate)

	// Status + notes only: the due date is kept.
	var updated struct {
		Status      string  `json:"status"`
		DueDate     string  `json:"dueDate"`
		CompletedAt *string `json:"completedAt"`
		Notes       *string `json:"notes"`
	}
	r := s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
		"status": "in_progress", "notes": "started",
	})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Equal("2025-02-10", updated.DueDate)
	s.Require().Equal("started", *updated.Notes)
	s.Require().Nil(updated.CompletedAt)

	// Due-date override (date-only), bad format rejected.
	s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
		"status": "in_progress", "dueDate": "10/02/2025",
	}).AssertStatus(s.T(), http.StatusBadRequest)
	r = s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
		"status": "in_progress", "dueDate": "2025-02-12",
	})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Equal("2025-02-12", updated.DueDate)

	// Completing (no approval required) stamps completed_at; reopening clears it.
	r = s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "completed"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().NotNil(updated.CompletedAt)
	s.Require().Equal("2025-02-12", updated.DueDate) // omitted dueDate keeps the override

	r = s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "in_progress"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Nil(updated.CompletedAt)
}
