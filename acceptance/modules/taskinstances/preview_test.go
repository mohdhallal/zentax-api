package taskinstances_test

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type previewTask struct {
	TemplateID     string  `json:"templateId"`
	PeriodCode     string  `json:"periodCode"`
	Name           string  `json:"name"`
	TaskType       string  `json:"taskType"`
	AssigneeName   *string `json:"assigneeName"`
	DueDate        string  `json:"dueDate"`
	PeriodEndDate  string  `json:"periodEndDate"`
	FilingDeadline string  `json:"filingDeadline"`
	OrderIndex     int     `json:"orderIndex"`
}

type workflowPreview struct {
	WorkflowID        string        `json:"workflowId"`
	WorkflowName      string        `json:"workflowName"`
	TotalPeriods      int           `json:"totalPeriods"`
	TaskTemplates     int           `json:"taskTemplates"`
	TotalTasks        int           `json:"totalTasks"`
	AssigneesImpacted []string      `json:"assigneesImpacted"`
	Tasks             []previewTask `json:"tasks"`
}

type instanceRow struct {
	WorkflowTaskID string `json:"workflowTaskId"`
	PeriodCode     string `json:"periodCode"`
	DueDate        string `json:"dueDate"`
	PeriodEndDate  string `json:"periodEndDate"`
	FilingDeadline string `json:"filingDeadline"`
}

// seedRecurringWorkflow builds entity -> obligation type -> recurring workflow
// (monthly, FY2025, filing = period end + 15d) and returns the workflow id.
func (s *TaskInstancesSuite) seedRecurringWorkflow(tenant, name string, periods []string) string {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": "Acme " + name, "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT " + name, "code": "VAT-" + name, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025",
		"selectedPeriods": periods,
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)
	return wf.ID
}

func (s *TaskInstancesSuite) addTemplate(tenant, workflowID, name, role, reference string, offset int, direction string, orderIndex int) string {
	var t struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": name, "taskType": "preparation", "roleLabel": role,
		"dueDateReference": reference, "dueDateOffsetValue": offset,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": direction, "orderIndex": orderIndex,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &t)
	return t.ID
}

// TestPreviewIsADryRunOfStart: GET /workflows/{id}/preview lists exactly the
// instances start would create — in selectedPeriods order (M10 after M2), then
// template order — as date-only values, and creates nothing.
func (s *TaskInstancesSuite) TestPreviewIsADryRunOfStart() {
	tenant := s.InsertTenant("pv-a", "Preview Tenant").String()
	wfID := s.seedRecurringWorkflow(tenant, "Preview VAT", []string{"M1", "M2", "M10"})
	// Inserted out of order on purpose: the planner must sort by orderIndex.
	prepareID := s.addTemplate(tenant, wfID, "Prepare", "Tax Manager", "filing_deadline", 5, "before", 1)
	collectID := s.addTemplate(tenant, wfID, "Collect", "Tax Analyst", "period_end", 2, "after", 0)

	var preview workflowPreview
	r := s.As(tenant).GET(s.T(), "/workflows/"+wfID+"/preview")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &preview)

	s.Require().Equal(wfID, preview.WorkflowID)
	s.Require().Equal("Preview VAT", preview.WorkflowName)
	s.Require().Equal(3, preview.TotalPeriods)
	s.Require().Equal(2, preview.TaskTemplates)
	s.Require().Equal(6, preview.TotalTasks)
	s.Require().Equal([]string{"Tax Analyst", "Tax Manager"}, preview.AssigneesImpacted)
	s.Require().Len(preview.Tasks, 6)

	// Period-major in selectedPeriods order, template orderIndex within a period.
	var order []string
	for _, t := range preview.Tasks {
		order = append(order, t.PeriodCode+":"+t.Name)
	}
	s.Require().Equal([]string{"M1:Collect", "M1:Prepare", "M2:Collect", "M2:Prepare", "M10:Collect", "M10:Prepare"}, order)

	// M1: period end Jan 31; filing Feb 15 (+15d); Collect due Feb 2 (+2d from
	// period end); Prepare due Feb 10 (-5d from filing).
	s.Require().Equal(collectID, preview.Tasks[0].TemplateID)
	s.Require().Equal("2025-01-31", preview.Tasks[0].PeriodEndDate)
	s.Require().Equal("2025-02-15", preview.Tasks[0].FilingDeadline)
	s.Require().Equal("2025-02-02", preview.Tasks[0].DueDate)
	s.Require().Equal("Tax Analyst", *preview.Tasks[0].AssigneeName)
	s.Require().Equal(prepareID, preview.Tasks[1].TemplateID)
	s.Require().Equal("2025-02-10", preview.Tasks[1].DueDate)
	// M10: Oct 31 -> filing Nov 15 -> Prepare due Nov 10.
	s.Require().Equal("2025-10-31", preview.Tasks[5].PeriodEndDate)
	s.Require().Equal("2025-11-10", preview.Tasks[5].DueDate)

	// Pure read: nothing was created.
	var instances []instanceRow
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID).DecodeData(s.T(), &instances)
	s.Require().Empty(instances)

	// Start (no body) persists exactly what preview showed.
	var start struct {
		InstancesCreated int `json:"instancesCreated"`
	}
	r = s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &start)
	s.Require().Equal(6, start.InstancesCreated)

	// Start is the draft → active lifecycle transition.
	var status struct {
		Status string `json:"status"`
	}
	s.As(tenant).GET(s.T(), "/workflows/"+wfID).DecodeData(s.T(), &status)
	s.Require().Equal("active", status.Status)

	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID+"&limit=100").DecodeData(s.T(), &instances)
	s.Require().Len(instances, 6)
	persisted := map[string]instanceRow{}
	for _, inst := range instances {
		persisted[inst.WorkflowTaskID+"_"+inst.PeriodCode] = inst
	}
	for _, t := range preview.Tasks {
		got, ok := persisted[t.TemplateID+"_"+t.PeriodCode]
		s.Require().True(ok, "preview row %s/%s was not persisted", t.PeriodCode, t.Name)
		s.Require().Equal(t.DueDate, got.DueDate)
		s.Require().Equal(t.PeriodEndDate, got.PeriodEndDate)
		s.Require().Equal(t.FilingDeadline, got.FilingDeadline)
	}

	// Preview still works after start (it is a read), and start is still guarded.
	s.As(tenant).GET(s.T(), "/workflows/"+wfID+"/preview").AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusConflict)
}

// TestStartAppliesTaskOverrides: a periodEndDate override moves one instance's
// period end and recomputes its filing deadline + due date; a dueDate override
// replaces the due date only; unknown keys / bad dates are rejected before
// anything is created; {} behaves like no body.
func (s *TaskInstancesSuite) TestStartAppliesTaskOverrides() {
	tenant := s.InsertTenant("ov-a", "Override Tenant").String()
	wfID := s.seedRecurringWorkflow(tenant, "Override VAT", []string{"M1", "M2"})
	collectID := s.addTemplate(tenant, wfID, "Collect", "Tax Analyst", "period_end", 2, "after", 0)
	prepareID := s.addTemplate(tenant, wfID, "Prepare", "Tax Manager", "filing_deadline", 5, "before", 1)

	post := func(body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", body)
	}
	var instances []instanceRow
	listInstances := func() []instanceRow {
		instances = nil
		s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID+"&limit=100").DecodeData(s.T(), &instances)
		return instances
	}

	// Unknown pair -> 400, nothing created.
	r := post(map[string]any{"taskOverrides": map[string]any{
		"bogus_M1": map[string]any{"dueDate": "2025-02-01"},
	}})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "unknown task override: bogus_M1")
	s.Require().Empty(listInstances())

	// Unparsable date -> 400, nothing created.
	post(map[string]any{"taskOverrides": map[string]any{
		prepareID + "_M1": map[string]any{"dueDate": "2025-13-40"},
	}}).AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Empty(listInstances())

	// Real overrides.
	r = post(map[string]any{"taskOverrides": map[string]any{
		prepareID + "_M1": map[string]any{"dueDate": "2025-02-12"},
		collectID + "_M2": map[string]any{"periodEndDate": "2025-02-20"},
	}})
	r.AssertStatus(s.T(), http.StatusCreated)

	byKey := map[string]instanceRow{}
	for _, inst := range listInstances() {
		byKey[inst.WorkflowTaskID+"_"+inst.PeriodCode] = inst
	}
	s.Require().Len(byKey, 4)

	// dueDate override: due moved, period end / filing untouched.
	s.Require().Equal("2025-02-12", byKey[prepareID+"_M1"].DueDate)
	s.Require().Equal("2025-01-31", byKey[prepareID+"_M1"].PeriodEndDate)
	s.Require().Equal("2025-02-15", byKey[prepareID+"_M1"].FilingDeadline)
	// periodEndDate override: Feb 20 -> filing Mar 7 (+15d) -> Collect due Feb 22 (+2d).
	s.Require().Equal("2025-02-20", byKey[collectID+"_M2"].PeriodEndDate)
	s.Require().Equal("2025-03-07", byKey[collectID+"_M2"].FilingDeadline)
	s.Require().Equal("2025-02-22", byKey[collectID+"_M2"].DueDate)
	// Untouched siblings keep the planned dates.
	s.Require().Equal("2025-02-02", byKey[collectID+"_M1"].DueDate)
	s.Require().Equal("2025-02-28", byKey[prepareID+"_M2"].PeriodEndDate)

	// {} is a valid "no overrides" body — on a fresh workflow it starts normally.
	wf2 := s.seedRecurringWorkflow(tenant, "Plain VAT", []string{"M1"})
	s.addTemplate(tenant, wf2, "Prepare", "Tax Manager", "filing_deadline", 5, "before", 0)
	s.As(tenant).POST(s.T(), "/workflows/"+wf2+"/start", map[string]any{}).AssertStatus(s.T(), http.StatusCreated)
}

// TestPreviewSharesStartValidation: a recurring workflow without an entity
// cannot be previewed (same 400 as start), and preview is tenant-scoped.
func (s *TaskInstancesSuite) TestPreviewSharesStartValidation() {
	tenant := s.InsertTenant("pv-b", "Preview Tenant B").String()
	other := s.InsertTenant("pv-c", "Preview Tenant C").String()

	var wf struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": "No entity", "workflowCategory": "recurring",
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = s.As(tenant).GET(s.T(), "/workflows/"+wf.ID+"/preview")
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "recurring workflow has no entity")

	s.As(other).GET(s.T(), "/workflows/"+wf.ID+"/preview").AssertStatus(s.T(), http.StatusNotFound)
}
