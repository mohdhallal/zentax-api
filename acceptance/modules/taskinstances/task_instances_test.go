package taskinstances_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type TaskInstancesSuite struct {
	acceptance.Suite
}

func TestTaskInstancesSuite(t *testing.T) {
	suite.Run(t, new(TaskInstancesSuite))
}

// TestWorkflowStartGeneratesInstances drives the full chain — entity ->
// obligation type -> recurring workflow -> task template -> POST .../start ->
// generated task instances — and verifies the computed legal-date deadlines
// (round-tripped through Postgres DATE columns, ADR-0002), idempotency (409),
// and tenant isolation (ADR-0004).
func (s *TaskInstancesSuite) TestWorkflowStartGeneratesInstances() {
	tenant := s.InsertTenant("ti-a", "TI Tenant A").String()
	other := s.InsertTenant("ti-b", "TI Tenant B").String()

	post := func(path string, body any) *acceptance.TestResponse {
		return s.Client.External().WithTenant(tenant).POST(s.T(), path, body)
	}

	var entity struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": "Acme", "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	var obType struct {
		ID string `json:"id"`
	}
	r = post("/obligation-types", map[string]any{"name": "VAT", "code": "VAT-RET", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	var wf struct {
		ID string `json:"id"`
	}
	r = post("/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025",
		"selectedPeriods": []string{"M1", "M2"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	// One task template: due 5 days before the filing deadline.
	post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	// Start -> 2 periods x 1 template = 2 instances.
	var start struct {
		InstancesCreated int `json:"instancesCreated"`
	}
	r = post("/workflows/"+wf.ID+"/start", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &start)
	s.Require().Equal(2, start.InstancesCreated)

	// Verify the computed deadlines (ordered by due_date asc).
	var list []struct {
		PeriodCode     string `json:"periodCode"`
		DueDate        string `json:"dueDate"`
		FilingDeadline string `json:"filingDeadline"`
		PeriodEndDate  string `json:"periodEndDate"`
	}
	s.Client.External().WithTenant(tenant).GET(s.T(), "/task-instances").DecodeData(s.T(), &list)
	s.Require().Len(list, 2)

	// M1: period end Jan 31, filing Feb 15 (+15d), task due Feb 10 (-5d).
	s.Require().Equal("M1", list[0].PeriodCode)
	s.Require().Equal("2025-01-31", list[0].PeriodEndDate)
	s.Require().Equal("2025-02-15", list[0].FilingDeadline)
	s.Require().Equal("2025-02-10", list[0].DueDate)
	// M2: task due Mar 10.
	s.Require().Equal("2025-03-10", list[1].DueDate)

	// Idempotency: starting again is rejected.
	post("/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusConflict)

	// Isolation: the other tenant sees no instances.
	var otherList []map[string]any
	s.Client.External().WithTenant(other).GET(s.T(), "/task-instances").DecodeData(s.T(), &otherList)
	s.Require().Empty(otherList)
}
