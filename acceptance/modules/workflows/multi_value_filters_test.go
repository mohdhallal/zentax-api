package workflows_test

import (
	"encoding/json"
	"net/http"
	"sort"
)

type workflowListPage struct {
	Names []string
	Pg    struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

func (s *WorkflowsSuite) listWorkflows(tenant, query string) workflowListPage {
	r := s.As(tenant).GET(s.T(), "/workflows"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status bool `json:"status"`
		Data   []struct {
			Name string `json:"name"`
		} `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := workflowListPage{}
	for _, w := range env.Data {
		out.Names = append(out.Names, w.Name)
	}
	sort.Strings(out.Names)
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

func (s *WorkflowsSuite) createRecurring(tenant, name, entity, obType, financialYear string) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entity, "obligationTypeId": obType,
		"periodicity": "monthly", "financialYear": financialYear, "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func (s *WorkflowsSuite) createProject(tenant, name string) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": name, "workflowCategory": "project", "projectType": "advisory",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

// setStatus moves a workflow to the given status through PUT (a full
// replacement, so the recurring/project shape is re-sent).
func (s *WorkflowsSuite) setStatus(tenant, id, status string, body map[string]any) {
	body["status"] = status
	s.As(tenant).PUT(s.T(), "/workflows/"+id, body).AssertStatus(s.T(), http.StatusOK)
}

// TestMultiValueStatusAndFinancialYearFilters: status and financialYear are
// repeatable. ?status=active&status=draft is the workflows page's "ongoing";
// ?financialYear=2026&financialYear=none is one fiscal year plus the project
// workflows, whose financial_year is NULL — the sentinel keeps them inside a
// year scope. The total counts the union, a single value still works, and a
// bad element is a 400.
func (s *WorkflowsSuite) TestMultiValueStatusAndFinancialYearFilters() {
	tenant := s.InsertTenant("wf-mv", "WF Multi Value").String()
	other := s.InsertTenant("wf-mv-b", "WF Multi Value B").String()
	entity := s.createEntity(tenant, "Acme MV")
	obType := s.createObligationType(tenant, "VAT-MV")

	recurringBody := func(name, fy string) map[string]any {
		return map[string]any{
			"name": name, "workflowCategory": "recurring",
			"entityId": entity, "obligationTypeId": obType,
			"periodicity": "monthly", "financialYear": fy, "selectedPeriods": []string{"M1"},
			"dueDateRule": map[string]any{
				"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
			},
		}
	}

	// FY2026: one draft, one active, one completed. FY2025: one draft.
	// Projects (no financial year): one draft, one archived.
	s.createRecurring(tenant, "VAT 2026 draft", entity, obType, "2026")
	active26 := s.createRecurring(tenant, "VAT 2026 active", entity, obType, "2026")
	s.setStatus(tenant, active26, "active", recurringBody("VAT 2026 active", "2026"))
	done26 := s.createRecurring(tenant, "VAT 2026 completed", entity, obType, "2026")
	s.setStatus(tenant, done26, "completed", recurringBody("VAT 2026 completed", "2026"))
	s.createRecurring(tenant, "VAT 2025 draft", entity, obType, "2025")
	s.createProject(tenant, "Audit project draft")
	archived := s.createProject(tenant, "Advisory project archived")
	s.setStatus(tenant, archived, "archived", map[string]any{
		"name": "Advisory project archived", "workflowCategory": "project", "projectType": "advisory",
	})
	// Noise in another tenant: never counted (RLS).
	s.createProject(other, "Other tenant project")

	// Unfiltered baseline.
	all := s.listWorkflows(tenant, "")
	s.Require().Equal(6, all.Pg.Total)
	s.Require().Len(all.Names, 6)
	s.Require().False(all.Pg.HasMore)

	// status=active&status=draft → the union, and total counts both.
	ongoing := s.listWorkflows(tenant, "?status=active&status=draft")
	s.Require().Equal([]string{"Audit project draft", "VAT 2025 draft", "VAT 2026 active", "VAT 2026 draft"}, ongoing.Names)
	s.Require().Equal(4, ongoing.Pg.Total)

	// The total is the count of the SAME predicate: page it with limit=1.
	one := s.listWorkflows(tenant, "?status=active&status=draft&limit=1")
	s.Require().Len(one.Names, 1)
	s.Require().Equal(4, one.Pg.Total)
	s.Require().True(one.Pg.HasMore)

	// A single status still works (one-element slice → ANY).
	activeOnly := s.listWorkflows(tenant, "?status=active")
	s.Require().Equal([]string{"VAT 2026 active"}, activeOnly.Names)
	s.Require().Equal(1, activeOnly.Pg.Total)

	// financialYear=2026&financialYear=none → FY2026 rows plus the projects
	// (NULL financial year); FY2025 is excluded.
	scope := s.listWorkflows(tenant, "?financialYear=2026&financialYear=none")
	s.Require().Equal([]string{
		"Advisory project archived", "Audit project draft",
		"VAT 2026 active", "VAT 2026 completed", "VAT 2026 draft",
	}, scope.Names)
	s.Require().Equal(5, scope.Pg.Total)

	// none alone → only the NULL rows; a plain year → only that year.
	noneOnly := s.listWorkflows(tenant, "?financialYear=none")
	s.Require().Equal([]string{"Advisory project archived", "Audit project draft"}, noneOnly.Names)
	s.Require().Equal(2, noneOnly.Pg.Total)
	fy25 := s.listWorkflows(tenant, "?financialYear=2025")
	s.Require().Equal([]string{"VAT 2025 draft"}, fy25.Names)
	s.Require().Equal(1, fy25.Pg.Total)

	// Filters combine: the ongoing work inside the FY2026 scope.
	combined := s.listWorkflows(tenant, "?financialYear=2026&financialYear=none&status=active&status=draft")
	s.Require().Equal([]string{"Audit project draft", "VAT 2026 active", "VAT 2026 draft"}, combined.Names)
	s.Require().Equal(3, combined.Pg.Total)

	// A year nobody has → empty page, zero total, no more.
	none := s.listWorkflows(tenant, "?financialYear=1999")
	s.Require().Empty(none.Names)
	s.Require().Equal(0, none.Pg.Total)
	s.Require().False(none.Pg.HasMore)

	// Each repeated element is validated: one bad value rejects the request.
	s.As(tenant).GET(s.T(), "/workflows?status=active&status=bogus").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/workflows?status=bogus").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/workflows?financialYear=2026&financialYear=0123456789").AssertStatus(s.T(), http.StatusBadRequest)

	// The other tenant sees only its own row under the same scope.
	otherScope := s.listWorkflows(other, "?financialYear=2026&financialYear=none")
	s.Require().Equal([]string{"Other tenant project"}, otherScope.Names)
	s.Require().Equal(1, otherScope.Pg.Total)
}
