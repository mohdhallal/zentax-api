package taskinstances_test

import (
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type projectPreviewTask struct {
	TemplateID      string  `json:"templateId"`
	PeriodCode      string  `json:"periodCode"`
	Name            string  `json:"name"`
	TaskType        string  `json:"taskType"`
	AssigneeName    *string `json:"assigneeName"`
	DueDate         string  `json:"dueDate"`
	PeriodEndDate   string  `json:"periodEndDate"`
	FilingDeadline  string  `json:"filingDeadline"`
	PaymentDeadline *string `json:"paymentDeadline"`
	OrderIndex      int     `json:"orderIndex"`
}

type projectPreview struct {
	WorkflowID        string               `json:"workflowId"`
	TotalPeriods      int                  `json:"totalPeriods"`
	TaskTemplates     int                  `json:"taskTemplates"`
	TotalTasks        int                  `json:"totalTasks"`
	AssigneesImpacted []string             `json:"assigneesImpacted"`
	Tasks             []projectPreviewTask `json:"tasks"`
}

type projectInstance struct {
	ID              string  `json:"id"`
	WorkflowTaskID  string  `json:"workflowTaskId"`
	PeriodCode      string  `json:"periodCode"`
	Name            string  `json:"name"`
	TaskType        string  `json:"taskType"`
	Status          string  `json:"status"`
	DueDate         string  `json:"dueDate"`
	PeriodEndDate   string  `json:"periodEndDate"`
	FilingDeadline  string  `json:"filingDeadline"`
	PaymentDeadline *string `json:"paymentDeadline"`
}

// seedProject creates a project workflow (no entity, no periods) and returns
// its id; endDate may be nil.
func (s *TaskInstancesSuite) seedProject(tenant, name string, endDate *string) string {
	body := map[string]any{
		"name": name, "workflowCategory": "project", "projectType": "audit_verification", "startDate": "2025-05-01",
	}
	if endDate != nil {
		body["endDate"] = *endDate
	}
	var wf struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflows", body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)
	return wf.ID
}

func (s *TaskInstancesSuite) addProjectTemplate(tenant, workflowID, name, taskType, role, reference string, offset int, direction string, orderIndex int) string {
	var t struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": name, "taskType": taskType, "roleLabel": role,
		"dueDateReference": reference, "dueDateOffsetValue": offset,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": direction, "orderIndex": orderIndex,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &t)
	return t.ID
}

func (s *TaskInstancesSuite) projectInstances(tenant, workflowID string) []projectInstance {
	var list []projectInstance
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+workflowID+"&limit=100").DecodeData(s.T(), &list)
	return list
}

func (s *TaskInstancesSuite) workflowStatus(tenant, workflowID string) string {
	var wf struct {
		Status string `json:"status"`
	}
	s.As(tenant).GET(s.T(), "/workflows/"+workflowID).DecodeData(s.T(), &wf)
	return wf.Status
}

func (s *TaskInstancesSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// TestProjectWorkflowStartGeneratesInstances: a project workflow (no entity,
// no fiscal periods) previews and starts into ONE instance per template under
// the "PROJECT" period — period end = filing deadline = the workflow's endDate,
// no payment deadline (a payment_deadline reference resolves to the end date),
// due = end date ± template offset — flips draft → active, audits
// workflow.started, and is guarded against a second start.
func (s *TaskInstancesSuite) TestProjectWorkflowStartGeneratesInstances() {
	tenant := s.InsertTenant("pj-a", "Project Tenant").String()
	end := "2025-06-30"
	wfID := s.seedProject(tenant, "Transfer pricing audit", &end)
	// Inserted out of order on purpose: the planner must sort by orderIndex.
	closeID := s.addProjectTemplate(tenant, wfID, "Close file", "other", "", "period_end", 5, "after", 2)
	payID := s.addProjectTemplate(tenant, wfID, "Pay advisor", "payment", "Finance", "payment_deadline", 3, "before", 1)
	fieldworkID := s.addProjectTemplate(tenant, wfID, "Fieldwork", "preparation", "Tax Manager", "filing_deadline", 10, "before", 0)

	// ---- preview: the plan, nothing written.
	var preview projectPreview
	r := s.As(tenant).GET(s.T(), "/workflows/"+wfID+"/preview")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &preview)
	s.Require().Equal(1, preview.TotalPeriods)
	s.Require().Equal(3, preview.TaskTemplates)
	s.Require().Equal(3, preview.TotalTasks)
	s.Require().Equal([]string{"Tax Manager", "Finance"}, preview.AssigneesImpacted)
	s.Require().Len(preview.Tasks, 3)
	s.Require().Equal([]string{fieldworkID, payID, closeID},
		[]string{preview.Tasks[0].TemplateID, preview.Tasks[1].TemplateID, preview.Tasks[2].TemplateID})
	for _, t := range preview.Tasks {
		s.Require().Equal("PROJECT", t.PeriodCode)
		s.Require().Equal(end, t.PeriodEndDate)
		s.Require().Equal(end, t.FilingDeadline)
		s.Require().Nil(t.PaymentDeadline, "a project has no payment deadline")
	}
	s.Require().Equal("2025-06-20", preview.Tasks[0].DueDate, "filing_deadline − 10d")
	s.Require().Equal("2025-06-27", preview.Tasks[1].DueDate, "payment_deadline → end date − 3d")
	s.Require().Equal("2025-07-05", preview.Tasks[2].DueDate, "period_end + 5d")
	s.Require().Empty(s.projectInstances(tenant, wfID))
	s.Require().Equal("draft", s.workflowStatus(tenant, wfID))

	// ---- start: exactly the preview, persisted; draft → active.
	var start struct {
		InstancesCreated int `json:"instancesCreated"`
	}
	r = s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &start)
	s.Require().Equal(3, start.InstancesCreated)
	s.Require().Equal("active", s.workflowStatus(tenant, wfID))

	instances := s.projectInstances(tenant, wfID)
	s.Require().Len(instances, 3)
	byTemplate := map[string]projectInstance{}
	for _, inst := range instances {
		byTemplate[inst.WorkflowTaskID] = inst
		s.Require().Equal("PROJECT", inst.PeriodCode)
		s.Require().Equal("not_started", inst.Status)
		s.Require().Equal(end, inst.PeriodEndDate)
		s.Require().Equal(end, inst.FilingDeadline)
		s.Require().Nil(inst.PaymentDeadline)
	}
	s.Require().Equal("2025-06-20", byTemplate[fieldworkID].DueDate)
	s.Require().Equal("2025-06-27", byTemplate[payID].DueDate)
	s.Require().Equal("payment", byTemplate[payID].TaskType)
	s.Require().Equal("2025-07-05", byTemplate[closeID].DueDate)
	for _, t := range preview.Tasks {
		s.Require().Equal(t.DueDate, byTemplate[t.TemplateID].DueDate, "preview row %s was not persisted as shown", t.Name)
	}
	// The period filter knows the code.
	var filtered []projectInstance
	s.As(tenant).GET(s.T(), "/task-instances?periodCode=PROJECT").DecodeData(s.T(), &filtered)
	s.Require().Len(filtered, 3)

	// ---- audit: workflow.started, one period.
	var details struct {
		Instances string `db:"instances"`
		Periods   string `db:"periods"`
	}
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&details,
			`SELECT details->>'instancesCreated' AS instances, details->>'periods' AS periods
			 FROM audit_log WHERE action = 'workflow.started' AND resource_id = $1`, wfID))
	})
	s.Require().Equal("3", details.Instances)
	s.Require().Equal("1", details.Periods)

	// ---- guarded: a second start is a 409; preview keeps working.
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusConflict)
	s.As(tenant).GET(s.T(), "/workflows/"+wfID+"/preview").AssertStatus(s.T(), http.StatusOK)
	s.Require().Len(s.projectInstances(tenant, wfID), 3)
}

// TestProjectWorkflowNeedsAnEndDate: without an end date a project can be
// neither previewed nor started (same 400), nothing is created and the
// workflow stays draft; setting the end date unblocks it.
func (s *TaskInstancesSuite) TestProjectWorkflowNeedsAnEndDate() {
	tenant := s.InsertTenant("pj-b", "Project Tenant B").String()
	wfID := s.seedProject(tenant, "Open-ended advisory", nil)
	s.addProjectTemplate(tenant, wfID, "Scope", "preparation", "", "filing_deadline", 0, "before", 0)

	r := s.As(tenant).GET(s.T(), "/workflows/"+wfID+"/preview")
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "project workflows need an end date")
	r = s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil)
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "project workflows need an end date")
	s.Require().Empty(s.projectInstances(tenant, wfID))
	s.Require().Equal("draft", s.workflowStatus(tenant, wfID))

	// Give it an end date → start works, the single instance is due on it.
	s.As(tenant).PUT(s.T(), "/workflows/"+wfID, map[string]any{
		"name": "Open-ended advisory", "workflowCategory": "project", "projectType": "advisory", "endDate": "2025-09-30",
	}).AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	instances := s.projectInstances(tenant, wfID)
	s.Require().Len(instances, 1)
	s.Require().Equal("2025-09-30", instances[0].DueDate)
	s.Require().Equal("2025-09-30", instances[0].FilingDeadline)
}

// TestProjectStartOverrides: overrides address "<templateId>_PROJECT" — dueDate
// replaces the due date; a paymentDeadline override (nothing to replace) and a
// period-shaped key are 400s that create nothing.
func (s *TaskInstancesSuite) TestProjectStartOverrides() {
	tenant := s.InsertTenant("pj-c", "Project Tenant C").String()
	end := "2025-06-30"
	wfID := s.seedProject(tenant, "Due diligence", &end)
	fieldworkID := s.addProjectTemplate(tenant, wfID, "Fieldwork", "preparation", "", "filing_deadline", 10, "before", 0)
	post := func(body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", body)
	}

	r := post(map[string]any{"taskOverrides": map[string]any{
		fieldworkID + "_PROJECT": map[string]any{"paymentDeadline": "2025-06-20"},
	}})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "project workflows have no payment deadline")
	s.Require().Empty(s.projectInstances(tenant, wfID))

	r = post(map[string]any{"taskOverrides": map[string]any{
		fieldworkID + "_M1": map[string]any{"dueDate": "2025-06-15"},
	}})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "unknown task override: "+fieldworkID+"_M1")
	s.Require().Empty(s.projectInstances(tenant, wfID))

	post(map[string]any{"taskOverrides": map[string]any{
		fieldworkID + "_PROJECT": map[string]any{"dueDate": "2025-06-15"},
	}}).AssertStatus(s.T(), http.StatusCreated)
	instances := s.projectInstances(tenant, wfID)
	s.Require().Len(instances, 1)
	s.Require().Equal("2025-06-15", instances[0].DueDate)
	s.Require().Equal(end, instances[0].FilingDeadline, "the override moves the due date only")
	s.Require().Nil(instances[0].PaymentDeadline)
}
