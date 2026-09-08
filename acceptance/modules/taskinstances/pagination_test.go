package taskinstances_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// pagedRow is the subset of a task-instance row the walks compare on. Both
// GET /task-instances and GET /reports/task-instances render these keys.
type pagedRow struct {
	ID         string `json:"id"`
	DueDate    string `json:"dueDate"`
	OrderIndex int    `json:"orderIndex"`
	CreatedAt  string `json:"createdAt"`
}

type pagedResponse struct {
	Body       string
	Rows       []pagedRow
	Pagination struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

func (s *TaskInstancesSuite) getPage(rb *acceptance.RequestBuilder, path string) pagedResponse {
	r := rb.GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool            `json:"status"`
		Data       []pagedRow      `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := pagedResponse{Body: body, Rows: env.Data}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pagination), body)
	return out
}

// walk pages `base` with the given limit from offset 0 to the end and returns
// the pages in order. Every page's pagination block is checked on the way:
// total is constant, limit/offset echo the request, hasMore is exact.
func (s *TaskInstancesSuite) walk(rb *acceptance.RequestBuilder, base string, limit int) []pagedResponse {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	var pages []pagedResponse
	offset := 0
	for {
		p := s.getPage(rb, fmt.Sprintf("%s%slimit=%d&offset=%d", base, sep, limit, offset))
		s.Require().Equal(limit, p.Pagination.Limit)
		s.Require().Equal(offset, p.Pagination.Offset)
		if len(pages) > 0 {
			s.Require().Equal(pages[0].Pagination.Total, p.Pagination.Total, "total must not drift between pages")
		}
		wantMore := offset+len(p.Rows) < p.Pagination.Total
		s.Require().Equal(wantMore, p.Pagination.HasMore, "hasMore at offset %d (%d rows of %d)", offset, len(p.Rows), p.Pagination.Total)
		if len(p.Rows) < limit {
			s.Require().False(p.Pagination.HasMore)
		}
		pages = append(pages, p)
		if !p.Pagination.HasMore {
			break
		}
		s.Require().Len(p.Rows, limit, "a page with more to come is full")
		offset += limit
		s.Require().LessOrEqual(offset, 1000, "runaway walk")
	}
	return pages
}

func idsOf(pages []pagedResponse) []string {
	var ids []string
	for _, p := range pages {
		for _, r := range p.Rows {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func rowsOf(pages []pagedResponse) []pagedRow {
	var rows []pagedRow
	for _, p := range pages {
		rows = append(rows, p.Rows...)
	}
	return rows
}

func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// requireDistinct asserts a walk yielded exactly `want` distinct ids and no
// id twice (a duplicate across page boundaries is the non-determinism bug).
func (s *TaskInstancesSuite) requireDistinct(ids []string, want int, what string) {
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	var dups []string
	for id, n := range seen {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	s.Require().Empty(dups, "%s: ids returned on more than one page", what)
	s.Require().Len(ids, want, "%s: total rows across pages", what)
	s.Require().Len(seen, want, "%s: distinct ids", what)
}

// requireSortedByDue asserts the concatenated pages are non-decreasing in
// (dueDate, id) — the generic list's order for the default / dueDate:asc sort
// (due_date, then the id tie-breaker). A violation exactly at a page boundary
// is what an unstable tie-break produces.
func (s *TaskInstancesSuite) requireSortedByDue(rows []pagedRow, what string) {
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		s.Require().LessOrEqual(a.DueDate+"|"+a.ID, b.DueDate+"|"+b.ID,
			"%s: rows %d and %d are out of (dueDate, id) order", what, i-1, i)
	}
}

// requireSortedByDueStep is the reports feed's order for sort=dueDate:asc:
// (due_date, order_index, id) — the six templates of a period keep their step
// order, then the id tie-breaker.
func (s *TaskInstancesSuite) requireSortedByDueStep(rows []pagedRow, what string) {
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		ka := fmt.Sprintf("%s|%04d|%s", a.DueDate, a.OrderIndex, a.ID)
		kb := fmt.Sprintf("%s|%04d|%s", b.DueDate, b.OrderIndex, b.ID)
		s.Require().LessOrEqual(ka, kb, "%s: rows %d and %d are out of (dueDate, orderIndex, id) order", what, i-1, i)
	}
}

// requireSortedByCreated asserts non-decreasing (createdAt, id) for the
// generic list under sort=createdAt:asc. Every row of one start shares
// created_at, so the id tie-break is what separates the pages.
func (s *TaskInstancesSuite) requireSortedByCreated(rows []pagedRow, what string) {
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		s.Require().LessOrEqual(a.CreatedAt+"|"+a.ID, b.CreatedAt+"|"+b.ID,
			"%s: rows %d and %d are out of (createdAt, id) order", what, i-1, i)
	}
}

// requireSortedByCreatedStep is the reports-feed variant: that feed orders
// createdAt walks by (created_at, order_index, id) — the template's step sits
// between the shared timestamp and the id tie-break.
func (s *TaskInstancesSuite) requireSortedByCreatedStep(rows []pagedRow, what string) {
	key := func(r pagedRow) string { return fmt.Sprintf("%s|%04d|%s", r.CreatedAt, r.OrderIndex, r.ID) }
	for i := 1; i < len(rows); i++ {
		s.Require().LessOrEqual(key(rows[i-1]), key(rows[i]),
			"%s: rows %d and %d are out of (createdAt, orderIndex, id) order", what, i-1, i)
	}
}

func (s *TaskInstancesSuite) requireByteIdentical(first, second []pagedResponse, what string) {
	s.Require().Len(second, len(first), "%s: page count differs between walks", what)
	for i := range first {
		s.Require().Equal(first[i].Body, second[i].Body, "%s: page %d differs between two identical walks", what, i)
	}
}

// seedDensePagingFixture builds one entity, one obligation type and TWO
// recurring monthly FY2025 workflows, each with 6 templates due exactly on
// the period end (period_end + 0 days) and started over 12 periods: 144
// instances in which 12 rows share every due date and each start's 72 rows
// share one created_at — the densest ties the feed sees. Returns the ids of
// the two workflows.
func (s *TaskInstancesSuite) seedDensePagingFixture(tenant string) []string {
	post := func(path string, body any) *acceptance.TestResponse {
		r := s.As(tenant).POST(s.T(), path, body)
		r.AssertStatus(s.T(), http.StatusCreated)
		return r
	}
	var entity, obType struct {
		ID string `json:"id"`
	}
	post("/entities", map[string]any{
		"name": "Acme Paging", "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	}).DecodeData(s.T(), &entity)
	post("/obligation-types", map[string]any{"name": "VAT Paging", "code": "VAT-PG", "template": "VAT"}).
		DecodeData(s.T(), &obType)

	periods := make([]string, 0, 12)
	for m := 1; m <= 12; m++ {
		periods = append(periods, fmt.Sprintf("M%d", m))
	}

	var workflows []string
	for w := 1; w <= 2; w++ {
		var wf struct {
			ID string `json:"id"`
		}
		post("/workflows", map[string]any{
			"name": fmt.Sprintf("Monthly VAT %d", w), "workflowCategory": "recurring",
			"entityId": entity.ID, "obligationTypeId": obType.ID,
			"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": periods,
			"dueDateRule": map[string]any{
				"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
			},
		}).DecodeData(s.T(), &wf)
		for t := 0; t < 6; t++ {
			post("/workflow-tasks", map[string]any{
				"workflowId": wf.ID, "name": fmt.Sprintf("Step %d", t), "taskType": "preparation",
				"dueDateReference": "period_end", "dueDateOffsetValue": 0,
				"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "after", "orderIndex": t,
			})
		}
		var start struct {
			InstancesCreated int `json:"instancesCreated"`
		}
		post("/workflows/"+wf.ID+"/start", nil).DecodeData(s.T(), &start)
		s.Require().Equal(72, start.InstancesCreated)
		workflows = append(workflows, wf.ID)
	}
	return workflows
}

// TestOffsetPagesAreDeterministicUnderDenseTies (PAG family, increment 2):
// with 12 instances per due date and 72 per created_at, every offset walk of
// both task feeds — under the default order and under sort=createdAt:asc —
// yields each of the 144 ids exactly once, in the promised order across page
// boundaries, and repeats byte-for-byte. Before the tie-breaker, a page
// boundary inside a tie could return the same row twice and skip another.
func (s *TaskInstancesSuite) TestOffsetPagesAreDeterministicUnderDenseTies() {
	tenant := s.InsertTenant("pg-a", "Paging Tenant").String()
	s.seedDensePagingFixture(tenant)
	admin := s.As(tenant)

	// The reference set: two pages at the generic cap (100).
	reference := s.walk(admin, "/task-instances", 100)
	s.Require().Len(reference, 2)
	s.Require().Equal(144, reference[0].Pagination.Total)
	refIDs := idsOf(reference)
	s.requireDistinct(refIDs, 144, "limit=100 reference")
	s.requireSortedByDue(rowsOf(reference), "limit=100 reference")
	wantIDs := sortedCopy(refIDs)

	// The ties really are dense: 12 rows per due date, one created_at per start.
	byDue := map[string]int{}
	byCreated := map[string]int{}
	for _, r := range rowsOf(reference) {
		byDue[r.DueDate]++
		byCreated[r.CreatedAt]++
	}
	s.Require().Len(byDue, 12)
	for due, n := range byDue {
		s.Require().Equal(12, n, "instances due on %s", due)
	}
	s.Require().Len(byCreated, 2, "each start stamps one created_at on all its rows")

	// Multiple pages under the generic feed's default (due_date) order and
	// under createdAt:asc; page size 5 does not divide 144 or 12, so
	// boundaries fall inside ties.
	cases := []struct {
		name   string
		base   string
		limit  int
		sorted func([]pagedRow, string)
	}{
		{"task-instances default (dueDate)", "/task-instances", 5, s.requireSortedByDue},
		{"task-instances sort=dueDate:asc", "/task-instances?sort=dueDate:asc", 5, s.requireSortedByDue},
		{"task-instances sort=createdAt:asc", "/task-instances?sort=createdAt:asc", 5, s.requireSortedByCreated},
		{"reports/task-instances default (dueDate)", "/reports/task-instances", 7, s.requireSortedByDueStep},
		{"reports/task-instances sort=dueDate:asc", "/reports/task-instances?sort=dueDate:asc", 7, s.requireSortedByDueStep},
		{"reports/task-instances sort=createdAt:asc", "/reports/task-instances?sort=createdAt:asc", 7, s.requireSortedByCreatedStep},
	}
	for _, tc := range cases {
		first := s.walk(admin, tc.base, tc.limit)
		wantPages := (144 + tc.limit - 1) / tc.limit
		s.Require().Len(first, wantPages, "%s: pages", tc.name)
		s.Require().Equal(144, first[0].Pagination.Total, tc.name)

		ids := idsOf(first)
		s.requireDistinct(ids, 144, tc.name)
		s.Require().Equal(wantIDs, sortedCopy(ids), "%s: the union of the pages is the full set", tc.name)
		if tc.sorted != nil {
			tc.sorted(rowsOf(first), tc.name)
		}

		second := s.walk(admin, tc.base, tc.limit)
		s.requireByteIdentical(first, second, tc.name)
	}

	// The migration behind the walk: both pagination indexes exist (the
	// harness database is migrated by migrate.sh, so 20260908000022 is applied)
	// and the tenant-less ones they replaced are gone.
	var names []string
	s.Require().NoError(s.DB.Select(&names, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = 'task_instances'
		ORDER BY indexname`))
	s.Require().Contains(names, "idx_task_instances_tenant_due")
	s.Require().Contains(names, "idx_task_instances_tenant_open_due")
	s.Require().NotContains(names, "idx_task_instances_due_date")
	s.Require().NotContains(names, "idx_task_instances_status")
}
