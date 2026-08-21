package taskinstances_test

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// setupApprovalInstances builds entity → obligation-type → recurring workflow →
// an approval-required task template → start, and returns the generated task
// instance IDs (ordered by due date). Built as a tenant-wide admin.
func (s *TaskInstancesSuite) setupApprovalInstances(tenant string, periods []string) []string {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": "Acme", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT", "code": "VAT-RET", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": periods,
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances").DecodeData(s.T(), &list)
	ids := make([]string, len(list))
	for i, ti := range list {
		ids[i] = ti.ID
	}
	return ids
}

// TestApprovalHappyPathAndImmutability: a preparer submits, a reviewer approves,
// and the approved record is then frozen (ADR-0018).
func (s *TaskInstancesSuite) TestApprovalHappyPathAndImmutability() {
	tenant := s.InsertTenant("appr-a", "Approval A").String()
	ids := s.setupApprovalInstances(tenant, []string{"M1"})
	s.Require().Len(ids, 1)
	id := ids[0]

	var submitted struct {
		Status      string  `json:"status"`
		SubmittedBy *string `json:"submittedBy"`
	}
	rs := s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil)
	rs.AssertStatus(s.T(), http.StatusOK)
	rs.DecodeData(s.T(), &submitted)
	s.Require().Equal("pending_approval", submitted.Status)
	s.Require().NotNil(submitted.SubmittedBy)

	// A preparer holds task:submit but not task:approve → 403 at the capability gate.
	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+id+"/approve", nil).
		AssertStatus(s.T(), http.StatusForbidden)

	var approved struct {
		Status     string  `json:"status"`
		ApprovedBy *string `json:"approvedBy"`
	}
	ra := s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+id+"/approve", nil)
	ra.AssertStatus(s.T(), http.StatusOK)
	ra.DecodeData(s.T(), &approved)
	s.Require().Equal("completed", approved.Status)
	s.Require().NotNil(approved.ApprovedBy)

	// Frozen: editing an approved instance is rejected (409).
	s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "in_progress"}).
		AssertStatus(s.T(), http.StatusConflict)
}

// TestApprovalSeparationOfDuties: a manager holds both task:submit and
// task:approve, but still cannot approve their OWN submission; a different
// approver can (ADR-0012 SoD).
func (s *TaskInstancesSuite) TestApprovalSeparationOfDuties() {
	tenant := s.InsertTenant("appr-b", "Approval B").String()
	ids := s.setupApprovalInstances(tenant, []string{"M1"})
	id := ids[0]

	s.AsRole(tenant, "manager").POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)

	// The same manager cannot approve what they submitted.
	s.AsRole(tenant, "manager").POST(s.T(), "/task-instances/"+id+"/approve", nil).
		AssertStatus(s.T(), http.StatusForbidden)

	// A different approver (the tenant admin) can.
	s.As(tenant).POST(s.T(), "/task-instances/"+id+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)
}

// TestRejectReturnsToEditable: a reviewer rejects with a reason; the preparer can
// then edit and resubmit.
func (s *TaskInstancesSuite) TestRejectReturnsToEditable() {
	tenant := s.InsertTenant("appr-c", "Approval C").String()
	ids := s.setupApprovalInstances(tenant, []string{"M1"})
	id := ids[0]

	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)

	var rejected struct {
		Status          string  `json:"status"`
		RejectionReason *string `json:"rejectionReason"`
	}
	rr := s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+id+"/reject",
		map[string]any{"reason": "figures do not reconcile"})
	rr.AssertStatus(s.T(), http.StatusOK)
	rr.DecodeData(s.T(), &rejected)
	s.Require().Equal("in_progress", rejected.Status)
	s.Require().NotNil(rejected.RejectionReason)

	// Editable again: the preparer updates, then resubmits.
	s.AsRole(tenant, "preparer").PUT(s.T(), "/task-instances/"+id, map[string]any{"status": "in_progress"}).
		AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
}
