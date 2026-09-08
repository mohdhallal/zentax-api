package reports_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// ReportsSuite proves the read-only /reports views: the enriched task-instance
// list (instance + workflow + entity + obligation type + assignee name) and
// the per-workflow completion stats — both RLS-isolated per tenant.
type ReportsSuite struct {
	acceptance.Suite
}

func TestReportsSuite(t *testing.T) {
	suite.Run(t, new(ReportsSuite))
}

var dateOnly = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type reportRow struct {
	ID                 string  `json:"id"`
	WorkflowID         string  `json:"workflowId"`
	WorkflowTaskID     string  `json:"workflowTaskId"`
	PeriodCode         string  `json:"periodCode"`
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	AssigneeID         *string `json:"assigneeId"`
	AssigneeName       *string `json:"assigneeName"`
	DueDate            string  `json:"dueDate"`
	PeriodEndDate      string  `json:"periodEndDate"`
	FilingDeadline     string  `json:"filingDeadline"`
	PaymentDeadline    *string `json:"paymentDeadline"`
	ApprovalRequired   *bool   `json:"approvalRequired"`
	CompletedAt        *string `json:"completedAt"`
	OrderIndex         int     `json:"orderIndex"`
	TaxDataStatus      string  `json:"taxDataStatus"`
	CreatedAt          string  `json:"createdAt"`
	WorkflowName       string  `json:"workflowName"`
	WorkflowCategory   string  `json:"workflowCategory"`
	ProjectType        *string `json:"projectType"`
	FinancialYear      *string `json:"financialYear"`
	EntityID           *string `json:"entityId"`
	EntityName         *string `json:"entityName"`
	ObligationTypeID   *string `json:"obligationTypeId"`
	ObligationTypeName *string `json:"obligationTypeName"`
	TaxType            *string `json:"taxType"`
}

type pagination struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type workflowStat struct {
	TotalTasks        int     `json:"totalTasks"`
	CompletedTasks    int     `json:"completedTasks"`
	CompletionPercent int     `json:"completionPercent"`
	NextDueDate       *string `json:"nextDueDate"`
}

// decodePaginated unwraps {status, data, pagination} — DecodeData only yields data.
func (s *ReportsSuite) decodePaginated(r *acceptance.TestResponse, rows any) pagination {
	var env struct {
		Status     bool            `json:"status"`
		Data       json.RawMessage `json:"data"`
		Pagination pagination      `json:"pagination"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env))
	s.Require().True(env.Status, "body: %s", r.BodyString())
	s.Require().NoError(json.Unmarshal(env.Data, rows))
	return env.Pagination
}

type seeded struct {
	EntityID, ObligationTypeID, WorkflowID string
}

// seedRecurringWorkflow builds entity -> obligation type -> recurring monthly
// workflow (FY2025, filing = period end + 15d) and returns all three ids.
func (s *ReportsSuite) seedRecurringWorkflow(tenant, name string, periods []string) seeded {
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
	return seeded{EntityID: entity.ID, ObligationTypeID: obType.ID, WorkflowID: wf.ID}
}

func (s *ReportsSuite) addTemplate(
	tenant, workflowID, name, reference string, offset int, direction string, orderIndex int, approval bool,
) string {
	var t struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": name, "taskType": "preparation", "approvalRequired": approval,
		"dueDateReference": reference, "dueDateOffsetValue": offset,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": direction, "orderIndex": orderIndex,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &t)
	return t.ID
}

// TestTaskInstancesReport: 2 periods x 2 templates -> 4 enriched rows carrying
// workflow / entity / obligation-type context, sorted by dueDate asc, with
// date-only legal dates and a real boolean approvalRequired; filters narrow;
// the other tenant sees nothing.
func (s *ReportsSuite) TestTaskInstancesReport() {
	tenant := s.InsertTenant("rp-a", "Reports A").String()
	other := s.InsertTenant("rp-b", "Reports B").String()

	sd := s.seedRecurringWorkflow(tenant, "Monthly VAT", []string{"M1", "M2"})
	collectID := s.addTemplate(tenant, sd.WorkflowID, "Collect", "period_end", 2, "after", 0, false)
	prepareID := s.addTemplate(tenant, sd.WorkflowID, "Prepare", "filing_deadline", 5, "before", 1, true)
	s.As(tenant).POST(s.T(), "/workflows/"+sd.WorkflowID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var rows []reportRow
	r := s.As(tenant).GET(s.T(), "/reports/task-instances")
	r.AssertStatus(s.T(), http.StatusOK)
	pg := s.decodePaginated(r, &rows)
	s.Require().Equal(pagination{Total: 4, Limit: 100, Offset: 0}, pg)
	s.Require().Len(rows, 4)

	// Default order: dueDate asc (Collect M1 Feb 2, Prepare M1 Feb 10, Collect M2 Mar 2, Prepare M2 Mar 10).
	var order []string
	for _, row := range rows {
		order = append(order, row.PeriodCode+":"+row.Name+"@"+row.DueDate)
	}
	s.Require().Equal([]string{
		"M1:Collect@2025-02-02", "M1:Prepare@2025-02-10", "M2:Collect@2025-03-02", "M2:Prepare@2025-03-10",
	}, order)

	for _, row := range rows {
		s.Require().Equal(sd.WorkflowID, row.WorkflowID)
		s.Require().Equal("Monthly VAT", row.WorkflowName)
		s.Require().Equal("recurring", row.WorkflowCategory)
		s.Require().Nil(row.ProjectType)
		s.Require().NotNil(row.FinancialYear)
		s.Require().Equal("2025", *row.FinancialYear)
		s.Require().NotNil(row.EntityID)
		s.Require().Equal(sd.EntityID, *row.EntityID)
		s.Require().NotNil(row.EntityName)
		s.Require().Equal("Acme Monthly VAT", *row.EntityName)
		s.Require().NotNil(row.ObligationTypeID)
		s.Require().Equal(sd.ObligationTypeID, *row.ObligationTypeID)
		s.Require().NotNil(row.ObligationTypeName)
		s.Require().Equal("VAT Monthly VAT", *row.ObligationTypeName)
		s.Require().NotNil(row.TaxType)
		s.Require().Equal("VAT", *row.TaxType)
		s.Require().Nil(row.AssigneeID)
		s.Require().Nil(row.AssigneeName)
		s.Require().Equal("not_started", row.Status)
		s.Require().Equal("draft", row.TaxDataStatus)
		s.Require().Nil(row.CompletedAt)
		s.Require().Regexp(dateOnly, row.DueDate)
		s.Require().Regexp(dateOnly, row.PeriodEndDate)
		s.Require().Regexp(dateOnly, row.FilingDeadline)
		s.Require().Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`, row.CreatedAt)
		s.Require().NotNil(row.ApprovalRequired, "approvalRequired must be a boolean, never null")
		switch row.WorkflowTaskID {
		case collectID:
			s.Require().False(*row.ApprovalRequired)
		case prepareID:
			s.Require().True(*row.ApprovalRequired)
		default:
			s.Require().Fail("unexpected workflowTaskId " + row.WorkflowTaskID)
		}
	}
	s.Require().Equal("2025-01-31", rows[0].PeriodEndDate)
	s.Require().Equal("2025-02-15", rows[0].FilingDeadline)

	// Explicit sort: dueDate:desc reverses.
	rows = nil
	s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?sort=dueDate:desc"), &rows)
	s.Require().Equal("2025-03-10", rows[0].DueDate)
	s.Require().Equal("2025-02-02", rows[3].DueDate)

	// Filters.
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=in_progress"), &rows)
	s.Require().Empty(rows)
	s.Require().Zero(pg.Total)

	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=not_started&limit=3"), &rows)
	s.Require().Len(rows, 3)
	s.Require().Equal(pagination{Total: 4, Limit: 3, Offset: 0}, pg)

	// The pseudo-status `open` = everything not completed (all four, so far).
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=open"), &rows)
	s.Require().Len(rows, 4)
	s.Require().Equal(4, pg.Total)

	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?limit=3&offset=3"), &rows)
	s.Require().Len(rows, 1)
	s.Require().Equal("2025-03-10", rows[0].DueDate)
	s.Require().Equal(pagination{Total: 4, Limit: 3, Offset: 3}, pg)

	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?entityId="+sd.EntityID), &rows)
	s.Require().Equal(4, pg.Total)

	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId="+sd.WorkflowID), &rows)
	s.Require().Equal(4, pg.Total)

	// A second workflow narrows the workflowId filter.
	sd2 := s.seedRecurringWorkflow(tenant, "Quarterly CIT", []string{"M3"})
	s.addTemplate(tenant, sd2.WorkflowID, "File", "filing_deadline", 1, "before", 0, false)
	s.As(tenant).POST(s.T(), "/workflows/"+sd2.WorkflowID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances"), &rows)
	s.Require().Equal(5, pg.Total)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId="+sd2.WorkflowID), &rows)
	s.Require().Equal(1, pg.Total)
	s.Require().Equal("Quarterly CIT", rows[0].WorkflowName)
	s.Require().Equal("Acme Quarterly CIT", *rows[0].EntityName)

	// Validation.
	s.As(tenant).GET(s.T(), "/reports/task-instances?status=bogus").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/reports/task-instances?limit=501").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId=nope").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/reports/task-instances?sort=name:asc").AssertStatus(s.T(), http.StatusBadRequest)

	// Any tenant member may read (task:read); unauthenticated is rejected.
	s.AsRole(tenant, "viewer").GET(s.T(), "/reports/task-instances").AssertStatus(s.T(), http.StatusOK)
	s.Client.External().GET(s.T(), "/reports/task-instances").AssertStatus(s.T(), http.StatusUnauthorized)

	// RLS: the other tenant sees an empty list.
	rows = nil
	pg = s.decodePaginated(s.As(other).GET(s.T(), "/reports/task-instances"), &rows)
	s.Require().Empty(rows)
	s.Require().Zero(pg.Total)
}

// TestWorkflowStats: an object keyed by workflow id — counts + next due date
// for a started workflow, zeros/null for one without instances; completing an
// instance moves the counters; the other tenant gets {}.
func (s *ReportsSuite) TestWorkflowStats() {
	tenant := s.InsertTenant("ws-a", "Stats A").String()
	other := s.InsertTenant("ws-b", "Stats B").String()

	sd := s.seedRecurringWorkflow(tenant, "Monthly VAT", []string{"M1", "M2"})
	s.addTemplate(tenant, sd.WorkflowID, "Collect", "period_end", 2, "after", 0, false)
	s.addTemplate(tenant, sd.WorkflowID, "Prepare", "filing_deadline", 5, "before", 1, false)
	s.As(tenant).POST(s.T(), "/workflows/"+sd.WorkflowID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	// A second workflow that is never started: present, at zero.
	empty := s.seedRecurringWorkflow(tenant, "Annual CIT", []string{"M12"})

	stats := map[string]workflowStat{}
	r := s.As(tenant).GET(s.T(), "/reports/workflow-stats")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &stats)
	s.Require().Len(stats, 2)

	got := stats[sd.WorkflowID]
	s.Require().Equal(4, got.TotalTasks)
	s.Require().Equal(0, got.CompletedTasks)
	s.Require().Equal(0, got.CompletionPercent)
	s.Require().NotNil(got.NextDueDate)
	s.Require().Equal("2025-02-02", *got.NextDueDate)

	zero := stats[empty.WorkflowID]
	s.Require().Equal(workflowStat{TotalTasks: 0, CompletedTasks: 0, CompletionPercent: 0, NextDueDate: nil}, zero)

	// Complete the earliest instance (Collect M1, due Feb 2) -> 1/4 = 25%, next = Feb 10.
	var rows []reportRow
	s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId="+sd.WorkflowID), &rows)
	s.Require().Equal("2025-02-02", rows[0].DueDate)
	s.As(tenant).PUT(s.T(), "/task-instances/"+rows[0].ID, map[string]any{"status": "completed"}).
		AssertStatus(s.T(), http.StatusOK)

	stats = map[string]workflowStat{}
	s.As(tenant).GET(s.T(), "/reports/workflow-stats").DecodeData(s.T(), &stats)
	got = stats[sd.WorkflowID]
	s.Require().Equal(4, got.TotalTasks)
	s.Require().Equal(1, got.CompletedTasks)
	s.Require().Equal(25, got.CompletionPercent)
	s.Require().NotNil(got.NextDueDate)
	s.Require().Equal("2025-02-10", *got.NextDueDate)

	// The enriched list reflects the completion too (completedAt stamped).
	rows = nil
	s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=completed"), &rows)
	s.Require().Len(rows, 1)
	s.Require().NotNil(rows[0].CompletedAt)

	// `status=open` is the complement — the dashboard's priority list is the
	// five soonest-due open instances: `status=open&sort=dueDate:asc&limit=5`.
	rows = nil
	pg := s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=open&sort=dueDate:asc&limit=5"), &rows)
	s.Require().Equal(pagination{Total: 3, Limit: 5, Offset: 0}, pg)
	s.Require().Len(rows, 3)
	s.Require().Equal("2025-02-10", rows[0].DueDate, "the completed Feb 2 instance is no longer open")
	for _, row := range rows {
		s.Require().NotEqual("completed", row.Status)
		s.Require().Nil(row.CompletedAt)
	}

	// Any tenant member may read (workflow:read).
	s.AsRole(tenant, "viewer").GET(s.T(), "/reports/workflow-stats").AssertStatus(s.T(), http.StatusOK)

	// RLS: the other tenant gets an empty OBJECT, not null.
	r = s.As(other).GET(s.T(), "/reports/workflow-stats")
	r.AssertStatus(s.T(), http.StatusOK)
	s.Require().Contains(r.BodyString(), `"data":{}`)
	stats = map[string]workflowStat{}
	r.DecodeData(s.T(), &stats)
	s.Require().Empty(stats)
}
