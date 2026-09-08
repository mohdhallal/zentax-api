package reports_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
)

// GET /reports/task-summary: the dashboard's tiles as ONE server-side
// aggregate against the tenant's civil day (ADR-0023 §6), so a tenant with
// more instances than any page cap still gets exact numbers.
//
// Fixture per tenant — one recurring workflow, FY2025 M1..M4 × {Collect,
// Review (approval required)} = 8 instances whose generated due dates all lie
// in 2025 — then the due dates of four of them are pinned to UTC-relative days
// that straddle midnight (yesterday / today / tomorrow / next week), one is
// completed, one submitted for approval, one set in progress and one blocked:
//
//	Collect M1  due U-1      not_started   Review M1  due U+8      not_started
//	Collect M2  due U        not_started   Review M2  due 2025-02  pending_approval
//	Collect M3  due U+1      not_started   Review M3  due 2025-03  in_progress
//	Collect M4  completed    (due 2025-05) Review M4  due 2025-04  blocked
//
// (U = today's UTC date.) The three overridden days classify differently in
// UTC, UTC+14 (Pacific/Kiritimati) and UTC−11 (Pacific/Pago_Pago), and at any
// UTC hour at least one of those zones is on a different day than UTC, so the
// tenant-day rule is proven on every run — the expectation is computed with
// Go's tz database from the same instant.

type taskSummary struct {
	Today            *string        `json:"today"`
	Total            int            `json:"total"`
	Completed        int            `json:"completed"`
	Active           int            `json:"active"`
	Overdue          int            `json:"overdue"`
	DueToday         int            `json:"dueToday"`
	DueThisWeek      int            `json:"dueThisWeek"`
	AwaitingApproval int            `json:"awaitingApproval"`
	CompletionRate   int            `json:"completionRate"`
	ByStatus         map[string]int `json:"byStatus"`
}

// summaryKeys / summaryStatuses are the keys every answer must carry.
var (
	summaryKeys = []string{
		"active", "awaitingApproval", "byStatus", "completed", "completionRate",
		"dueThisWeek", "dueToday", "overdue", "today", "total",
	}
	summaryStatuses = []string{"blocked", "completed", "in_progress", "in_review", "not_started", "pending_approval"}
)

// taskSummary GETs the summary and asserts the contract: 200, every key
// present, byStatus with all six statuses.
func (s *ReportsSuite) taskSummary(tenant, path string) taskSummary {
	r := s.As(tenant).GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)

	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &raw), r.BodyString())
	keys := make([]string, 0, len(raw.Data))
	for k := range raw.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.Require().Equal(summaryKeys, keys, "%s: every key is always present", path)
	var byStatus map[string]int
	s.Require().NoError(json.Unmarshal(raw.Data["byStatus"], &byStatus))
	statuses := make([]string, 0, len(byStatus))
	for k := range byStatus {
		statuses = append(statuses, k)
	}
	sort.Strings(statuses)
	s.Require().Equal(summaryStatuses, statuses, "%s: byStatus carries the six statuses", path)

	var out taskSummary
	r.DecodeData(s.T(), &out)
	s.Require().NotNil(out.Today, "%s: today is always set", path)
	s.Require().Regexp(dateOnly, *out.Today)
	return out
}

// summaryFixture is one tenant's eight-instance workflow.
type summaryFixture struct {
	entity, obligation, workflow string
	collectM1                    string // due U-1 (the assignee filter's instance)
}

func (s *ReportsSuite) seedSummaryFixture(tenant string, utcDay func(int) string) summaryFixture {
	f := summaryFixture{}
	f.entity = s.seedEntity(tenant, "Acme Summary", "Germany")
	f.obligation = s.seedObligationType(tenant, "VAT Return", "VAT-S", "VAT")
	f.workflow = s.seedWorkflowFor(tenant, "Summary VAT", f.entity, f.obligation, "2025", []string{"M1", "M2", "M3", "M4"})
	s.addTemplate(tenant, f.workflow, "Collect", "period_end", 2, "after", 0, false)
	s.addTemplate(tenant, f.workflow, "Review", "filing_deadline", 5, "before", 1, true)
	s.start(tenant, f.workflow)

	put := func(id string, body map[string]any) {
		s.As(tenant).PUT(s.T(), "/task-instances/"+id, body).AssertStatus(s.T(), http.StatusOK)
	}
	f.collectM1 = s.instanceID(tenant, f.workflow, "M1", "Collect")
	put(f.collectM1, map[string]any{"status": "not_started", "dueDate": utcDay(-1)})
	put(s.instanceID(tenant, f.workflow, "M2", "Collect"), map[string]any{"status": "not_started", "dueDate": utcDay(0)})
	put(s.instanceID(tenant, f.workflow, "M3", "Collect"), map[string]any{"status": "not_started", "dueDate": utcDay(1)})
	put(s.instanceID(tenant, f.workflow, "M4", "Collect"), map[string]any{"status": "completed"})
	put(s.instanceID(tenant, f.workflow, "M1", "Review"), map[string]any{"status": "not_started", "dueDate": utcDay(8)})
	s.As(tenant).POST(s.T(), "/task-instances/"+s.instanceID(tenant, f.workflow, "M2", "Review")+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
	put(s.instanceID(tenant, f.workflow, "M3", "Review"), map[string]any{"status": "in_progress"})
	put(s.instanceID(tenant, f.workflow, "M4", "Review"), map[string]any{"status": "blocked"})
	return f
}

// expectedSummary is the fixture's tile set for a tenant whose civil day is
// `today` (YYYY-MM-DD; the week ends on `weekEnd`, the Saturday): the three
// Review instances still due in 2025 are always overdue; the four pinned days
// fall into the buckets relative to that day.
func expectedSummary(today, weekEnd string, pinned []string) taskSummary {
	want := taskSummary{
		Total: 8, Completed: 1, Active: 7, Overdue: 3, AwaitingApproval: 1, CompletionRate: 13, // 12.5 rounds up
		ByStatus: map[string]int{
			"not_started": 4, "in_progress": 1, "in_review": 0, "pending_approval": 1, "completed": 1, "blocked": 1,
		},
	}
	for _, due := range pinned {
		switch {
		case due < today:
			want.Overdue++
		case due == today:
			want.DueToday++
		case due <= weekEnd:
			want.DueThisWeek++
		}
	}
	return want
}

func (s *ReportsSuite) assertSummary(zone string, want, got taskSummary) {
	s.Require().Equal(want.Total, got.Total, "%s total", zone)
	s.Require().Equal(want.Completed, got.Completed, "%s completed", zone)
	s.Require().Equal(want.Active, got.Active, "%s active", zone)
	s.Require().Equal(want.Overdue, got.Overdue, "%s overdue", zone)
	s.Require().Equal(want.DueToday, got.DueToday, "%s dueToday", zone)
	s.Require().Equal(want.DueThisWeek, got.DueThisWeek, "%s dueThisWeek", zone)
	s.Require().Equal(want.AwaitingApproval, got.AwaitingApproval, "%s awaitingApproval", zone)
	s.Require().Equal(want.CompletionRate, got.CompletionRate, "%s completionRate", zone)
	s.Require().Equal(want.ByStatus, got.ByStatus, "%s byStatus", zone)
}

func (s *ReportsSuite) TestTaskSummary() {
	now := time.Now().UTC()
	utcDay := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	pinned := []string{utcDay(-1), utcDay(0), utcDay(1), utcDay(8)}

	// ---- the tenant's civil day decides every due window.
	civil := func(zone string) (today, weekEnd string) {
		loc, err := time.LoadLocation(zone)
		s.Require().NoError(err)
		t := now.In(loc)
		return t.Format("2006-01-02"), t.AddDate(0, 0, 6-int(t.Weekday())).Format("2006-01-02")
	}
	utcToday, utcWeekEnd := civil("UTC")
	utcWant := expectedSummary(utcToday, utcWeekEnd, pinned)

	var utcTenant string
	var utcFixture summaryFixture
	flips := 0
	for i, zone := range []string{"UTC", "Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		tenant := s.InsertTenantWithTimezone("ts-"+string(rune('a'+i)), "Summary "+zone, zone).String()
		f := s.seedSummaryFixture(tenant, utcDay)
		today, weekEnd := civil(zone)
		want := expectedSummary(today, weekEnd, pinned)

		got := s.taskSummary(tenant, "/reports/task-summary")
		s.Require().Equal(today, *got.Today, "%s: today is the tenant's civil date", zone)
		s.assertSummary(zone, want, got)
		if want.Overdue != utcWant.Overdue || want.DueToday != utcWant.DueToday {
			flips++
		}
		if zone == "UTC" {
			utcTenant, utcFixture = tenant, f
		}
	}
	s.Require().Positive(flips, "at any UTC hour at least one of UTC+14 / UTC-11 is on a different day than UTC")

	// ---- consistency with the feed: the tiles describe the same population
	// the list pages, so a tile drill-down can never disagree with its rows.
	tenant, f := utcTenant, utcFixture
	summary := s.taskSummary(tenant, "/reports/task-summary")
	var rows []reportRow
	pg := s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=open&limit=500"), &rows)
	s.Require().Equal(summary.Active, pg.Total)
	s.Require().Len(rows, summary.Active)
	overdue, dueToday := 0, 0
	for _, row := range rows {
		s.Require().NotEqual("completed", row.Status)
		switch {
		case row.DueDate < *summary.Today:
			overdue++
		case row.DueDate == *summary.Today:
			dueToday++
		}
	}
	s.Require().Equal(summary.Overdue, overdue, "overdue == open rows due before today")
	s.Require().Equal(summary.DueToday, dueToday)
	rows = nil
	s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?status=completed"), &rows)
	s.Require().Len(rows, summary.Completed)

	// ---- filters (all shared with the feed). More population first: a second
	// entity's FY2026 workflow (one future instance), a project workflow with
	// no financial year (one overdue instance), and an assignee.
	other := s.seedEntity(tenant, "Acme Other", "France")
	cit := s.seedObligationType(tenant, "Corporate Tax", "CIT-S", "CIT")
	otherWf := s.seedWorkflowFor(tenant, "Other CIT", other, cit, "2026", []string{"M12"})
	s.addTemplate(tenant, otherWf, "File", "filing_deadline", 1, "before", 0, false)
	s.start(tenant, otherWf)
	project := s.postID(tenant, "/workflows", map[string]any{
		"name": "Restructuring", "workflowCategory": "project", "projectType": "restructuring",
		"entityId": f.entity, "startDate": "2025-01-15", "endDate": "2025-03-31",
	})
	s.addTemplate(tenant, project, "Draft memo", "filing_deadline", 10, "before", 0, false)
	s.start(tenant, project)
	var me struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.As(tenant).PUT(s.T(), "/task-instances/"+f.collectM1, map[string]any{
		"status": "not_started", "assigneeId": me.ID,
	}).AssertStatus(s.T(), http.StatusOK)

	// Unfiltered: 8 + 1 + 1 instances; the project's 2025-03-21 due date is overdue.
	all := s.taskSummary(tenant, "/reports/task-summary")
	s.Require().Equal(10, all.Total)
	s.Require().Equal(1, all.Completed)
	s.Require().Equal(9, all.Active)
	s.Require().Equal(utcWant.Overdue+1, all.Overdue)
	s.Require().Equal(utcWant.DueToday, all.DueToday)
	s.Require().Equal(utcWant.DueThisWeek, all.DueThisWeek)
	s.Require().Equal(1, all.AwaitingApproval)
	s.Require().Equal(10, all.CompletionRate)
	s.Require().Equal(map[string]int{
		"not_started": 6, "in_progress": 1, "in_review": 0, "pending_approval": 1, "completed": 1, "blocked": 1,
	}, all.ByStatus)

	get := func(query string) taskSummary { return s.taskSummary(tenant, "/reports/task-summary?"+query) }

	// entityId / workflowId.
	got := get("entityId=" + f.entity)
	s.Require().Equal(9, got.Total)
	s.Require().Equal(utcWant.Overdue+1, got.Overdue)
	s.Require().Equal(5, got.ByStatus["not_started"])
	got = get("entityId=" + other)
	s.Require().Equal(taskSummary{Today: got.Today, Total: 1, Active: 1, ByStatus: map[string]int{
		"not_started": 1, "in_progress": 0, "in_review": 0, "pending_approval": 0, "completed": 0, "blocked": 0,
	}}, got)
	got = get("workflowId=" + f.workflow)
	s.assertSummary("workflowId", utcWant, got)

	// assigneeId: one instance (Collect M1, due yesterday UTC → overdue); an
	// unknown assignee is an empty set with today still set.
	got = get("assigneeId=" + me.ID)
	s.Require().Equal(1, got.Total)
	s.Require().Equal(1, got.Overdue)
	s.Require().Equal(0, got.DueToday)
	got = get("assigneeId=" + uuid.NewString())
	s.Require().Equal(0, got.Total)
	s.Require().Equal(0, got.CompletionRate)
	s.Require().Equal(utcToday, *got.Today)

	// financialYear: repeatable, `none` = no financial year (the project).
	s.Require().Equal(8, get("financialYear=2025").Total)
	s.Require().Equal(1, get("financialYear=2026").Total)
	got = get("financialYear=none")
	s.Require().Equal(1, got.Total)
	s.Require().Equal(1, got.Overdue)
	s.Require().Equal(9, get("financialYear=2025&financialYear=none").Total)
	s.Require().Equal(9, get("financialYear=2025&financialYear=2026").Total)
	s.Require().Equal(10, get("financialYear=2025&financialYear=2026&financialYear=none").Total)
	s.Require().Equal(0, get("financialYear=1999").Total)
	s.Require().Equal(10, get("financialYear=all").Total, "the legacy \"all\" means no filter")
	s.Require().Equal(10, get("financialYear=").Total)

	// status (incl. the pseudo-value open) / workflowCategory / combinations.
	got = get("status=open")
	s.Require().Equal(9, got.Total)
	s.Require().Equal(0, got.Completed)
	s.Require().Equal(9, got.Active)
	s.Require().Equal(0, got.CompletionRate)
	got = get("status=completed")
	s.Require().Equal(1, got.Total)
	s.Require().Equal(1, got.Completed)
	s.Require().Equal(0, got.Active)
	s.Require().Equal(0, got.Overdue, "a completed instance is never overdue, whatever its due date")
	s.Require().Equal(100, got.CompletionRate)
	got = get("status=pending_approval")
	s.Require().Equal(1, got.Total)
	s.Require().Equal(1, got.AwaitingApproval)
	s.Require().Equal(1, got.Overdue)
	s.Require().Equal(1, get("workflowCategory=project").Total)
	s.Require().Equal(9, get("workflowCategory=recurring").Total)
	s.Require().Equal(7, get("entityId="+f.entity+"&status=open&financialYear=2025").Total)

	// The feed honours the same filters.
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?financialYear=none"), &rows)
	s.Require().Equal(1, pg.Total)
	s.Require().Equal("PROJECT", rows[0].PeriodCode)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?assigneeId="+me.ID), &rows)
	s.Require().Equal(1, pg.Total)
	s.Require().Equal(f.collectM1, rows[0].ID)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowCategory=project&status=open"), &rows)
	s.Require().Equal(1, pg.Total)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?financialYear=2025&financialYear=2026&limit=3"), &rows)
	s.Require().Equal(9, pg.Total)
	s.Require().Len(rows, 3)

	// ---- validation.
	for _, query := range []string{"status=bogus", "assigneeId=nope", "workflowCategory=bogus", "entityId=nope"} {
		s.As(tenant).GET(s.T(), "/reports/task-summary?"+query).AssertStatus(s.T(), http.StatusBadRequest)
		s.As(tenant).GET(s.T(), "/reports/task-instances?"+query).AssertStatus(s.T(), http.StatusBadRequest)
	}

	// ---- authz: any tenant member (task:read) may read; anonymous is rejected.
	s.AsRole(tenant, "viewer").GET(s.T(), "/reports/task-summary").AssertStatus(s.T(), http.StatusOK)
	s.Client.External().GET(s.T(), "/reports/task-summary").AssertStatus(s.T(), http.StatusUnauthorized)

	// ---- RLS: another tenant sees zeros — with today still set (UTC, its default zone).
	otherTenant := s.InsertTenant("ts-other", "Summary Other").String()
	got = s.taskSummary(otherTenant, "/reports/task-summary")
	s.Require().Equal(taskSummary{Today: got.Today, ByStatus: map[string]int{
		"not_started": 0, "in_progress": 0, "in_review": 0, "pending_approval": 0, "completed": 0, "blocked": 0,
	}}, got)
	s.Require().Equal(utcToday, *got.Today)
}
