package workflowtasks_test

import (
	"encoding/json"
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// deleteChain is a started recurring workflow with one approval-required task
// template, so deleting the template row has real instances underneath it.
type deleteChain struct {
	WorkflowID     string
	WorkflowTaskID string
	Instances      []string
}

func (s *WorkflowTasksSuite) seedDeleteChain(tenant, name string, periods []string) deleteChain {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf, wt struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": name, "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT " + name, "code": "VAT-" + name, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": name + " VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": periods,
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wt)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &list)
	out := deleteChain{WorkflowID: wf.ID, WorkflowTaskID: wt.ID}
	for _, ti := range list {
		out.Instances = append(out.Instances, ti.ID)
	}
	s.Require().NotEmpty(out.Instances)
	return out
}

func (s *WorkflowTasksSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// TestDeleteRefusedWhenApprovedWorkExists: the easiest way to lose attested
// work was the one that looks least dangerous — removing a step from the
// template. task_instances.workflow_task_id cascades, so the delete used to
// take every instance generated from that step, approved ones included.
func (s *WorkflowTasksSuite) TestDeleteRefusedWhenApprovedWorkExists() {
	tenant := s.InsertTenant("wt-del-a", "WT Delete A").String()
	c := s.seedDeleteChain(tenant, "Acme", []string{"M1", "M2"})

	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+c.Instances[0]+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+c.Instances[0]+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)

	resp := s.As(tenant).DELETE(s.T(), "/workflow-tasks/"+c.WorkflowTaskID)
	resp.AssertStatus(s.T(), http.StatusConflict)
	resp.AssertErrorCode(s.T(), "CONFLICT")
	s.Require().Contains(resp.BodyString(), "1 approved task instance(s)")

	s.As(tenant).GET(s.T(), "/workflow-tasks/"+c.WorkflowTaskID).AssertStatus(s.T(), http.StatusOK)
	for _, id := range c.Instances {
		s.As(tenant).GET(s.T(), "/task-instances/"+id).AssertStatus(s.T(), http.StatusOK)
	}
}

// TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade: with nothing
// attested the step can still be removed — and the envelope says how many
// instances went with it.
func (s *WorkflowTasksSuite) TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade() {
	tenant := s.InsertTenant("wt-del-b", "WT Delete B").String()
	c := s.seedDeleteChain(tenant, "Beta", []string{"M1", "M2", "M3"})
	s.Require().Len(c.Instances, 3)

	s.As(tenant).DELETE(s.T(), "/workflow-tasks/"+c.WorkflowTaskID).AssertStatus(s.T(), http.StatusNoContent)

	s.As(tenant).GET(s.T(), "/workflow-tasks/"+c.WorkflowTaskID).AssertStatus(s.T(), http.StatusNotFound)
	for _, id := range c.Instances {
		s.As(tenant).GET(s.T(), "/task-instances/"+id).AssertStatus(s.T(), http.StatusNotFound)
	}

	var raw []byte
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&raw,
			`SELECT details FROM audit_log WHERE action = 'workflow_task.deleted' ORDER BY seq DESC LIMIT 1`))
	})
	details := map[string]any{}
	s.Require().NoError(json.Unmarshal(raw, &details))
	s.Require().Equal(float64(3), details["taskInstances"])
}
