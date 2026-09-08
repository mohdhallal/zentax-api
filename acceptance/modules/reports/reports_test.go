package reports_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
	s.As(tenant).GET(s.T(), "/reports/task-instances?sort=priority:asc").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/reports/task-instances?sort=name").AssertStatus(s.T(), http.StatusBadRequest)

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

// ---------------------------------------------------------------------------
// The task feed's server-side filters, due windows and sorts (increment 4):
// the tasks page no longer filters or sorts in the browser, so every choice it
// offers must be exact on the server — and the summary must count the same
// population the feed pages.
// ---------------------------------------------------------------------------

// feedFixture is the population the feed tests cut from — six instances:
//
//	Monthly VAT   (Acme 100% GmbH · VAT Return · FY2025 · M1–M2) × {Collect data, Prepare return}
//	              → M1 Collect 02-02, M1 Prepare 02-10, M2 Collect 03-02, M2 Prepare 03-10 (2025)
//	Annual CIT    (Under_score Holdings · Corporate Income Tax · FY2026 · M12) × {File return}
//	Restructuring (project · no entity · no financial year) × {Draft memo} → PROJECT, due 2025-03-21
//
// The two entity names carry the LIKE metacharacters the search must treat
// literally; the project has neither entity nor obligation type nor year.
type feedFixture struct {
	entityA, entityB, vat, cit, monthly, annual, project string
}

func (s *ReportsSuite) seedFeedFixture(tenant string) feedFixture {
	f := feedFixture{}
	f.entityA = s.seedEntity(tenant, "Acme 100% GmbH", "Germany")
	f.entityB = s.seedEntity(tenant, "Under_score Holdings", "France")
	f.vat = s.seedObligationType(tenant, "VAT Return", "VAT-F", "VAT")
	f.cit = s.seedObligationType(tenant, "Corporate Income Tax", "CIT-F", "CIT")
	f.monthly = s.seedWorkflowFor(tenant, "Monthly VAT", f.entityA, f.vat, "2025", []string{"M1", "M2"})
	s.addTemplate(tenant, f.monthly, "Collect data", "period_end", 2, "after", 0, false)
	s.addTemplate(tenant, f.monthly, "Prepare return", "filing_deadline", 5, "before", 1, true)
	s.start(tenant, f.monthly)
	f.annual = s.seedWorkflowFor(tenant, "Annual CIT", f.entityB, f.cit, "2026", []string{"M12"})
	s.addTemplate(tenant, f.annual, "File return", "filing_deadline", 1, "before", 0, false)
	s.start(tenant, f.annual)
	f.project = s.postID(tenant, "/workflows", map[string]any{
		"name": "Restructuring memo", "workflowCategory": "project", "projectType": "restructuring",
		"startDate": "2025-01-15", "endDate": "2025-03-31",
	})
	s.addTemplate(tenant, f.project, "Draft memo", "filing_deadline", 10, "before", 0, false)
	s.start(tenant, f.project)
	return f
}

// feed GETs the feed with the query and returns the rows and the exact total.
// Without an explicit limit the fixture fits one page, so the total — counted
// over the base set, without the display joins — must equal the rows returned.
func (s *ReportsSuite) feed(tenant, query string) ([]reportRow, int) {
	var rows []reportRow
	r := s.As(tenant).GET(s.T(), "/reports/task-instances?"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	pg := s.decodePaginated(r, &rows)
	if !strings.Contains(query, "limit=") {
		s.Require().Len(rows, pg.Total, "%s: the exact total must agree with the rows", query)
	}
	return rows, pg.Total
}

func feedIDs(rows []reportRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func feedKeys(rows []reportRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.PeriodCode+":"+row.Name+"@"+row.DueDate)
	}
	return out
}

// TestTaskFeedFilters: obligationTypeId / taxType / periodCode / dueFrom /
// dueTo / search / assigneeId (me, unassigned) narrow the feed AND the summary
// identically; validation rejects what the contract does not admit.
func (s *ReportsSuite) TestTaskFeedFilters() {
	tenant := s.InsertTenant("ff-a", "Feed Filters").String()
	f := s.seedFeedFixture(tenant)
	get := func(query string) ([]reportRow, int) { return s.feed(tenant, query) }
	total := func(query string) int {
		_, n := get(query)
		return n
	}
	summaryTotal := func(query string) int { return s.taskSummary(tenant, "/reports/task-summary?"+query).Total }
	bad := func(query string) {
		s.As(tenant).GET(s.T(), "/reports/task-instances?"+query).AssertStatus(s.T(), http.StatusBadRequest)
		s.As(tenant).GET(s.T(), "/reports/task-summary?"+query).AssertStatus(s.T(), http.StatusBadRequest)
	}

	all, n := get("")
	s.Require().Equal(6, n)
	byKey := map[string]reportRow{}
	for _, row := range all {
		byKey[row.WorkflowName+"/"+row.PeriodCode+"/"+row.Name] = row
	}

	// ---- obligationTypeId / taxType (the obligation type's template). A
	// project workflow has neither, so it never matches either.
	s.Require().Equal(4, total("obligationTypeId="+f.vat))
	s.Require().Equal(1, total("obligationTypeId="+f.cit))
	s.Require().Equal(0, total("obligationTypeId="+uuid.NewString()))
	rows, _ := get("taxType=VAT")
	s.Require().Len(rows, 4)
	for _, row := range rows {
		s.Require().Equal("VAT", *row.TaxType)
	}
	s.Require().Equal(1, total("taxType=CIT"))
	s.Require().Equal(0, total("taxType=TP"))
	s.Require().Equal(0, total("taxType=Custom"))
	s.Require().Equal(4, summaryTotal("taxType=VAT"))
	s.Require().Equal(1, summaryTotal("obligationTypeId="+f.cit))
	bad("obligationTypeId=nope")
	bad("taxType=vat")

	// ---- periodCode.
	rows, n = get("periodCode=M1")
	s.Require().Equal(2, n)
	for _, row := range rows {
		s.Require().Equal("M1", row.PeriodCode)
	}
	s.Require().Equal(1, total("periodCode=M12"))
	s.Require().Equal(1, total("periodCode=PROJECT"))
	s.Require().Equal(0, total("periodCode=M13"))
	s.Require().Equal(2, summaryTotal("periodCode=M1"))
	bad("periodCode=" + strings.Repeat("P", 17))

	// ---- dueFrom / dueTo: inclusive bounds on the due date (YYYY-MM-DD).
	rows, n = get("dueFrom=2025-02-10&dueTo=2025-03-02")
	s.Require().Equal(2, n)
	s.Require().Equal([]string{"M1:Prepare return@2025-02-10", "M2:Collect data@2025-03-02"}, feedKeys(rows))
	s.Require().Equal(1, total("dueTo=2025-02-02"))
	from := 0
	for _, row := range all {
		if row.DueDate >= "2025-03-10" {
			from++
		}
	}
	s.Require().Equal(3, from, "M2 Prepare, the project memo and the FY2026 filing")
	s.Require().Equal(from, total("dueFrom=2025-03-10"))
	s.Require().Equal(from, summaryTotal("dueFrom=2025-03-10"))
	s.Require().Equal(0, total("dueFrom=2025-03-03&dueTo=2025-03-01"), "an inverted window is empty, not an error")
	bad("dueFrom=2025-13-01")
	bad("dueTo=2025-02-30")
	bad("dueFrom=yesterday")
	bad("dueFrom=")  // strict, like the feed's uuid params: a blank bound is not "no bound"
	bad("dueTo=all") // the legacy "all" belongs to the report pages' filters, not the feed's

	// ---- search: a literal, case-insensitive substring over the task name,
	// period code, workflow name, entity name and obligation-type name — the
	// LIKE metacharacters are not wildcards.
	q := func(term string) string { return "search=" + url.QueryEscape(term) }
	rows, n = get(q("collect"))
	s.Require().Equal(2, n)
	for _, row := range rows {
		s.Require().Equal("Collect data", row.Name)
	}
	s.Require().Equal(2, total(q("COLLECT")), "case-insensitive")
	s.Require().Equal(2, total(q("  collect  ")), "trimmed")
	s.Require().Equal(1, total(q("annual")), "workflow name")
	s.Require().Equal(1, total(q("M12")), "period code")
	s.Require().Equal(1, total(q("project")), "the project's PROJECT period code")
	s.Require().Equal(1, total(q("corporate")), "obligation-type name")
	rows, n = get(q("100%"))
	s.Require().Equal(4, n, "entity name")
	for _, row := range rows {
		s.Require().Equal("Acme 100% GmbH", *row.EntityName)
	}
	s.Require().Equal(4, total(q("%")), "% is not a wildcard: only the entity name carrying a literal % matches")
	s.Require().Equal(1, total(q("_")), "_ is not a wildcard: only the entity name carrying a literal _ matches")
	s.Require().Equal(1, total(q("Under_score")))
	s.Require().Equal(0, total(q("UnderXscore")), "_ does not match an arbitrary character")
	s.Require().Equal(0, total(q(`\`)), "the escape character is literal too")
	s.Require().Equal(0, total(q("Müller")))
	s.Require().Equal(6, total(q("")), "a blank search is no filter")
	s.Require().Equal(6, total(q("   ")))
	s.Require().Equal(4, summaryTotal(q("100%")), "the summary searches the same way")
	s.Require().Equal(2, total(q("collect")+"&status=open&financialYear=2025"), "composes with the other filters")
	s.Require().Equal(0, total(q(strings.Repeat("a", 200))), "at the cap")
	bad(q(strings.Repeat("a", 201)))

	// ---- assigneeId: a user id, `me` (the session user) or `unassigned`.
	var me struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	mine := byKey["Monthly VAT/M1/Collect data"].ID
	s.As(tenant).PUT(s.T(), "/task-instances/"+mine, map[string]any{"status": "not_started", "assigneeId": me.ID}).
		AssertStatus(s.T(), http.StatusOK)
	rows, n = get("assigneeId=me")
	s.Require().Equal(1, n)
	s.Require().Equal([]string{mine}, feedIDs(rows))
	rows, _ = get("assigneeId=" + me.ID)
	s.Require().Equal([]string{mine}, feedIDs(rows), "me is the session user's own id")
	rows, n = get("assigneeId=unassigned")
	s.Require().Equal(5, n)
	for _, row := range rows {
		s.Require().Nil(row.AssigneeID)
	}
	s.Require().Equal(1, summaryTotal("assigneeId=me"))
	s.Require().Equal(5, summaryTotal("assigneeId=unassigned"))
	// `me` is whoever asks: another member has nothing assigned.
	var viewerRows []reportRow
	pg := s.decodePaginated(s.AsRole(tenant, "viewer").GET(s.T(), "/reports/task-instances?assigneeId=me"), &viewerRows)
	s.Require().Zero(pg.Total)
	s.Require().Empty(viewerRows)
	pg = s.decodePaginated(s.AsRole(tenant, "viewer").GET(s.T(), "/reports/task-instances?assigneeId=unassigned&limit=1"), &viewerRows)
	s.Require().Equal(5, pg.Total, "unassigned is the same set for every member")
	bad("assigneeId=someone")

	// ---- everything at once.
	rows, n = get("taxType=VAT&periodCode=M1&assigneeId=unassigned&financialYear=2025&status=open&" + q("prepare"))
	s.Require().Equal(1, n)
	s.Require().Equal("Prepare return", rows[0].Name)
}

// TestTaskFeedDueWindowsUseTenantDay: due=overdue|today|thisWeek is the very
// predicate the summary tile counts, evaluated against the TENANT's civil day
// — proven on UTC+14 (Pacific/Kiritimati) and UTC−11 (Pacific/Pago_Pago),
// where at any UTC hour at least one is on a different day than the server.
// The fixture is the summary's (four due dates pinned to U−1 / U / U+1 / U+8,
// three instances overdue since 2025, one completed).
func (s *ReportsSuite) TestTaskFeedDueWindowsUseTenantDay() {
	now := time.Now().UTC()
	utcDay := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	pinned := []string{utcDay(-1), utcDay(0), utcDay(1), utcDay(8)}
	utcToday := utcDay(0)

	var tenant string
	flips := 0
	for i, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		tenant = s.InsertTenantWithTimezone("fd-"+string(rune('a'+i)), "Feed "+zone, zone).String()
		s.seedSummaryFixture(tenant, utcDay)
		loc, err := time.LoadLocation(zone)
		s.Require().NoError(err)
		local := now.In(loc)
		today := local.Format("2006-01-02")
		weekEnd := local.AddDate(0, 0, 6-int(local.Weekday())).Format("2006-01-02")
		want := expectedSummary(today, weekEnd, pinned)
		if today != utcToday {
			flips++
		}

		summary := s.taskSummary(tenant, "/reports/task-summary")
		s.Require().Equal(today, *summary.Today, "%s: today is the tenant's civil date", zone)

		windows := []struct {
			due  string
			want int
			tile int
			in   func(d string) bool
		}{
			{"overdue", want.Overdue, summary.Overdue, func(d string) bool { return d < today }},
			{"today", want.DueToday, summary.DueToday, func(d string) bool { return d == today }},
			{"thisWeek", want.DueThisWeek, summary.DueThisWeek, func(d string) bool { return d > today && d <= weekEnd }},
		}
		for _, w := range windows {
			var rows []reportRow
			pg := s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?due="+w.due+"&limit=500"), &rows)
			s.Require().Equal(w.want, pg.Total, "%s due=%s: total per Go's tz database", zone, w.due)
			s.Require().Equal(w.tile, pg.Total, "%s due=%s: the drill-down IS the tile", zone, w.due)
			s.Require().Len(rows, w.want)
			for _, row := range rows {
				s.Require().NotEqual("completed", row.Status, "%s due=%s: a completed instance is never in a window", zone, w.due)
				s.Require().True(w.in(row.DueDate), "%s due=%s: %s is outside the window of the tenant's day %s (week end %s)",
					zone, w.due, row.DueDate, today, weekEnd)
			}
			// Composable: the window never admits a completed instance,
			// whatever its due date.
			rows = nil
			pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?due="+w.due+"&status=completed"), &rows)
			s.Require().Zero(pg.Total, "%s due=%s&status=completed", zone, w.due)
			s.Require().Zero(s.taskSummary(tenant, "/reports/task-summary?due="+w.due+"&status=completed").Total)
			// The summary narrowed to a window counts only that window.
			narrowed := s.taskSummary(tenant, "/reports/task-summary?due="+w.due)
			s.Require().Equal(w.want, narrowed.Total, "%s due=%s: summary total", zone, w.due)
			s.Require().Equal(0, narrowed.Completed)
		}
	}
	s.Require().Positive(flips, "at any UTC hour at least one of UTC+14 / UTC-11 is on a different day than UTC")

	s.As(tenant).GET(s.T(), "/reports/task-instances?due=someday").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/reports/task-summary?due=tomorrow").AssertStatus(s.T(), http.StatusBadRequest)
}

// TestTaskFeedSortsAreDeterministic: every sort key, in both directions,
// orders by its primary column and yields the SAME pages on two walks whose
// concatenation is exactly the whole list — the tie-breakers (due date, order
// index, id, all in the primary direction) make offset paging stable where
// ties are dense. Instances without an entity sort last either way.
func (s *ReportsSuite) TestTaskFeedSortsAreDeterministic() {
	tenant := s.InsertTenant("fs-a", "Feed Sorts").String()
	f := s.seedFeedFixture(tenant)
	put := func(id, status string) {
		s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{"status": status}).AssertStatus(s.T(), http.StatusOK)
	}
	// Spread the statuses so the rank has something to order — and ties to break.
	put(s.instanceID(tenant, f.monthly, "M1", "Prepare return"), "in_progress")
	put(s.instanceID(tenant, f.monthly, "M2", "Collect data"), "completed")
	put(s.instanceID(tenant, f.project, "PROJECT", "Draft memo"), "blocked")

	rank := map[string]int{
		"not_started": 0, "in_progress": 1, "in_review": 2, "pending_approval": 3, "completed": 4, "blocked": 5,
	}
	primary := map[string]func(r reportRow) string{
		"dueDate":   func(r reportRow) string { return r.DueDate },
		"createdAt": func(r reportRow) string { return r.CreatedAt },
		"status":    func(r reportRow) string { return strconv.Itoa(rank[r.Status]) },
		"workflow":  func(r reportRow) string { return r.WorkflowName },
		"name":      func(r reportRow) string { return r.Name },
		"entity": func(r reportRow) string {
			if r.EntityName == nil {
				return ""
			}
			return *r.EntityName
		},
	}
	walk := func(spec string) []string {
		var ids []string
		for offset := 0; ; offset += 2 {
			var page []reportRow
			pg := s.decodePaginated(s.As(tenant).GET(s.T(),
				fmt.Sprintf("/reports/task-instances?sort=%s&limit=2&offset=%d", spec, offset)), &page)
			s.Require().Equal(6, pg.Total, spec)
			ids = append(ids, feedIDs(page)...)
			if len(page) < 2 {
				return ids
			}
		}
	}

	for key, value := range primary {
		for _, dir := range []string{"asc", "desc"} {
			spec := key + ":" + dir
			whole, n := s.feed(tenant, "sort="+spec)
			s.Require().Equal(6, n, spec)

			// The primary order holds; NULL entities are last either way.
			for i := 1; i < len(whole); i++ {
				a, b := value(whole[i-1]), value(whole[i])
				if key == "entity" {
					if a == "" {
						s.Require().Equal("", b, "%s: an instance without an entity must come last", spec)
						continue
					}
					if b == "" {
						continue
					}
				}
				if dir == "asc" {
					s.Require().LessOrEqual(a, b, "%s: %q before %q", spec, a, b)
				} else {
					s.Require().GreaterOrEqual(a, b, "%s: %q before %q", spec, a, b)
				}
			}
			if key == "entity" {
				s.Require().Nil(whole[5].EntityName, "%s: the project's instance is last", spec)
			}

			first, second := walk(spec), walk(spec)
			s.Require().Equal(feedIDs(whole), first, "%s: the pages concatenate to the whole list", spec)
			s.Require().Equal(first, second, "%s: two walks agree", spec)
			seen := map[string]bool{}
			for _, id := range first {
				s.Require().False(seen[id], "%s: %s appears on two pages", spec, id)
				seen[id] = true
			}
			s.Require().Len(seen, 6, spec)
		}
	}

	// asc and desc are exact reverses of each other — a mixed-direction
	// tie-break could not promise that.
	for key := range primary {
		asc, _ := s.feed(tenant, "sort="+key+":asc")
		desc, _ := s.feed(tenant, "sort="+key+":desc")
		if key == "entity" {
			// NULLS LAST in both directions: only the entity-bearing prefix reverses.
			asc, desc = asc[:5], desc[:5]
		}
		reversed := make([]string, 0, len(desc))
		for i := len(desc) - 1; i >= 0; i-- {
			reversed = append(reversed, desc[i].ID)
		}
		s.Require().Equal(feedIDs(asc), reversed, "%s: desc is the exact reverse of asc", key)
	}
}

// TestWorkflowStatsFilters: workflowId / entityId / financialYear (repeatable,
// `none`) / status / workflowCategory narrow the stats to a subset of the
// workflows; the shape stays an object keyed by workflow id ({} when nothing
// matches).
func (s *ReportsSuite) TestWorkflowStatsFilters() {
	tenant := s.InsertTenant("ws-f", "Stats Filters").String()
	f := s.seedFeedFixture(tenant)
	draft := s.seedWorkflowFor(tenant, "Quarterly VAT", f.entityA, f.vat, "2025", []string{"M3"}) // never started

	keys := func(query string) []string {
		stats := map[string]workflowStat{}
		r := s.As(tenant).GET(s.T(), "/reports/workflow-stats?"+query)
		r.AssertStatus(s.T(), http.StatusOK)
		r.DecodeData(s.T(), &stats)
		out := make([]string, 0, len(stats))
		for k := range stats {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	set := func(ids ...string) []string {
		out := append([]string{}, ids...)
		sort.Strings(out)
		return out
	}

	s.Require().Equal(set(f.monthly, f.annual, f.project, draft), keys(""))
	s.Require().Equal(set(f.monthly), keys("workflowId="+f.monthly))
	s.Require().Empty(keys("workflowId=" + uuid.NewString()))
	s.Require().Equal(set(f.monthly, draft), keys("entityId="+f.entityA))
	s.Require().Equal(set(f.annual), keys("entityId="+f.entityB))
	s.Require().Equal(set(f.monthly, draft), keys("financialYear=2025"))
	s.Require().Equal(set(f.annual), keys("financialYear=2026"))
	s.Require().Equal(set(f.project), keys("financialYear=none"), "none = no financial year (the project)")
	s.Require().Equal(set(f.monthly, draft, f.project), keys("financialYear=2025&financialYear=none"))
	s.Require().Equal(set(f.monthly, draft, f.annual), keys("financialYear=2025&financialYear=2026"))
	s.Require().Len(keys("financialYear=all"), 4, "the legacy all means no filter")
	s.Require().Len(keys("financialYear="), 4)
	s.Require().Empty(keys("financialYear=1999"))
	s.Require().Equal(set(draft), keys("status=draft"))
	s.Require().Equal(set(f.monthly, f.annual, f.project), keys("status=active"))
	s.Require().Empty(keys("status=archived"))
	s.Require().Equal(set(f.project), keys("workflowCategory=project"))
	s.Require().Equal(set(f.monthly, f.annual, draft), keys("workflowCategory=recurring"))
	s.Require().Equal(set(f.monthly), keys("entityId="+f.entityA+"&status=active"))

	// The shape is unchanged: counts + next due date per key; {} when empty.
	stats := map[string]workflowStat{}
	s.As(tenant).GET(s.T(), "/reports/workflow-stats?workflowId="+f.monthly).DecodeData(s.T(), &stats)
	s.Require().Equal(4, stats[f.monthly].TotalTasks)
	s.Require().Equal(0, stats[f.monthly].CompletedTasks)
	s.Require().NotNil(stats[f.monthly].NextDueDate)
	s.Require().Equal("2025-02-02", *stats[f.monthly].NextDueDate)
	r := s.As(tenant).GET(s.T(), "/reports/workflow-stats?financialYear=1999")
	r.AssertStatus(s.T(), http.StatusOK)
	s.Require().Contains(r.BodyString(), `"data":{}`)

	for _, query := range []string{"status=bogus", "entityId=nope", "workflowId=nope", "workflowCategory=bogus"} {
		s.As(tenant).GET(s.T(), "/reports/workflow-stats?"+query).AssertStatus(s.T(), http.StatusBadRequest)
	}
	s.AsRole(tenant, "viewer").GET(s.T(), "/reports/workflow-stats?status=active").AssertStatus(s.T(), http.StatusOK)
}

// The feed's sort is ONE key (the generic lists honour several); a second key
// is a 400 rather than silently ignored, so a client that copies the
// multi-sort form from /workflows learns about it immediately.
func (s *ReportsSuite) TestTaskFeedSortIsASingleKey() {
	tenant := s.InsertTenant("sort-one", "Sort One").String()
	s.As(tenant).GET(s.T(), "/reports/task-instances?sort=dueDate:asc").AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).GET(s.T(), "/reports/task-instances?sort=dueDate:asc&sort=name:desc").AssertStatus(s.T(), http.StatusBadRequest)
}
