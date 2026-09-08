package workflows_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// The three hostile names of the scale fixture (seed/demo/scale/shape.go).
const (
	hostileAmpersand  = "Müller & Söhne 100% GmbH"
	hostileUnderscore = "Under_score Holdings Ltd"
	hostileBackslash  = `O'Brien \ Partners`
)

type workflowRow struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	EntityID           *string `json:"entityId"`
	EntityName         *string `json:"entityName"`
	ObligationTypeID   *string `json:"obligationTypeId"`
	ObligationTypeName *string `json:"obligationTypeName"`
}

type rowsPage struct {
	Rows []workflowRow
	Pg   struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

func (p rowsPage) names() []string {
	out := make([]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		out = append(out, r.Name)
	}
	return out
}

func (p rowsPage) sortedNames() []string {
	names := p.names()
	sort.Strings(names)
	return names
}

func (s *WorkflowsSuite) listRows(tenant, query string) rowsPage {
	r := s.As(tenant).GET(s.T(), "/workflows"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool            `json:"status"`
		Data       []workflowRow   `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := rowsPage{Rows: env.Data}
	if out.Rows == nil {
		out.Rows = []workflowRow{}
	}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

func (s *WorkflowsSuite) search(tenant, term, extra string) rowsPage {
	return s.listRows(tenant, "?search="+url.QueryEscape(term)+extra)
}

func (s *WorkflowsSuite) createNamedObligationType(tenant, name, code string) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": name, "code": code, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func recurringWorkflowBody(name, entity, obType, fy string) map[string]any {
	return map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entity, "obligationTypeId": obType,
		"periodicity": "monthly", "financialYear": fy, "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	}
}

// TestSearchAndJoinedNames: list rows carry entityName / obligationTypeName
// (null on project workflows) from the LEFT-JOINed select; `search` is a
// literal, case-insensitive substring of the workflow name that narrows page
// and total together; every filter and sort keeps working over the
// `w.`-qualified columns (status / name exist on the joined tables too);
// blank is no search; over-long is a 400; RLS isolates tenants.
func (s *WorkflowsSuite) TestSearchAndJoinedNames() {
	tenant := s.InsertTenant("wf-search", "WF Search").String()
	other := s.InsertTenant("wf-search-b", "WF Search B").String()

	acme := s.createEntity(tenant, "Acme GmbH")
	beta := s.createEntity(tenant, "Beta Ltd")
	vat := s.createNamedObligationType(tenant, "VAT Return", "VAT-RET")
	cit := s.createNamedObligationType(tenant, "Corporate Income Tax", "CIT-A")

	// Five projects (no entity, no obligation type, no financial year) …
	for _, name := range []string{hostileAmpersand, hostileUnderscore, hostileBackslash, "Room 1001 Project", "UnderXscore Project"} {
		s.createProject(tenant, name)
	}
	// … and three recurring workflows; the CIT one is made active.
	vat26 := s.createRecurring(tenant, "VAT 2026 Acme", acme, vat, "2026")
	s.createRecurring(tenant, "VAT 2025 Acme", acme, vat, "2025")
	cit26 := s.createRecurring(tenant, "CIT 2026 Beta", beta, cit, "2026")
	s.setStatus(tenant, cit26, "active", recurringWorkflowBody("CIT 2026 Beta", beta, cit, "2026"))
	// Noise in another tenant, with the same searchable name.
	s.createProject(other, "VAT other tenant")
	const all = 8

	// --- joined names on every read ---
	base := s.listRows(tenant, "?limit=100")
	s.Require().Equal(all, base.Pg.Total)
	s.Require().Len(base.Rows, all)
	for _, row := range base.Rows {
		switch row.Name {
		case "VAT 2026 Acme", "VAT 2025 Acme":
			s.Require().Equal(acme, *row.EntityID)
			s.Require().Equal("Acme GmbH", *row.EntityName, row.Name)
			s.Require().Equal(vat, *row.ObligationTypeID)
			s.Require().Equal("VAT Return", *row.ObligationTypeName, row.Name)
		case "CIT 2026 Beta":
			s.Require().Equal("Beta Ltd", *row.EntityName)
			s.Require().Equal("Corporate Income Tax", *row.ObligationTypeName)
			s.Require().Equal("active", row.Status)
		default: // projects
			s.Require().Nil(row.EntityID, row.Name)
			s.Require().Nil(row.EntityName, row.Name)
			s.Require().Nil(row.ObligationTypeID, row.Name)
			s.Require().Nil(row.ObligationTypeName, row.Name)
		}
	}
	// GET by id and the write responses carry the names too (one contract).
	var one workflowRow
	s.As(tenant).GET(s.T(), "/workflows/"+vat26).DecodeData(s.T(), &one)
	s.Require().Equal("Acme GmbH", *one.EntityName)
	s.Require().Equal("VAT Return", *one.ObligationTypeName)
	r := s.As(tenant).POST(s.T(), "/workflows", recurringWorkflowBody("VAT 2024 Beta", beta, vat, "2024"))
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &one)
	s.Require().Equal("Beta Ltd", *one.EntityName)
	s.Require().Equal("VAT Return", *one.ObligationTypeName)
	r = s.As(tenant).PUT(s.T(), "/workflows/"+one.ID, recurringWorkflowBody("VAT 2024 Acme", acme, vat, "2024"))
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &one)
	s.Require().Equal("Acme GmbH", *one.EntityName)
	s.As(tenant).DELETE(s.T(), "/workflows/"+one.ID).AssertStatus(s.T(), http.StatusNoContent)

	// --- search narrows page AND total ---
	got := s.search(tenant, "vat", "")
	s.Require().Equal([]string{"VAT 2025 Acme", "VAT 2026 Acme"}, got.sortedNames())
	s.Require().Equal(2, got.Pg.Total)
	s.Require().Equal(got.sortedNames(), s.search(tenant, "VAT", "").sortedNames())
	for _, row := range got.Rows {
		s.Require().Equal("Acme GmbH", *row.EntityName)
	}

	cases := map[string][]string{
		"100%":               {hostileAmpersand},
		"& Söhne":            {hostileAmpersand},
		"müller":             {hostileAmpersand},
		"Under_score":        {hostileUnderscore},
		`\`:                  {hostileBackslash},
		`O'Brien \ Partners`: {hostileBackslash},
		"100":                {hostileAmpersand, "Room 1001 Project"},
		"Under":              {hostileUnderscore, "UnderXscore Project"},
		"nobody":             {},
	}
	for term, want := range cases {
		sort.Strings(want) // sortedNames() is bytewise; compare sets
		got = s.search(tenant, term, "")
		s.Require().Equal(want, got.sortedNames(), "search %q", term)
		s.Require().Equal(len(want), got.Pg.Total, "total for search %q", term)
	}
	// Entity / obligation-type names are NOT searched (name only).
	s.Require().Equal(0, s.search(tenant, "Acme GmbH", "").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "VAT Return", "").Pg.Total)

	// Blank search is no search.
	sorted := s.listRows(tenant, "?sort=createdAt:asc&limit=100")
	s.Require().Equal(sorted, s.listRows(tenant, "?sort=createdAt:asc&limit=100&search="))
	s.Require().Equal(sorted, s.listRows(tenant, "?sort=createdAt:asc&limit=100&search=%20%20"))

	// search + sort=name:asc + limit=1: the searched total, page by page.
	page1 := s.listRows(tenant, "?search=vat&sort=name:asc&limit=1")
	s.Require().Equal([]string{"VAT 2025 Acme"}, page1.names())
	s.Require().Equal(2, page1.Pg.Total)
	s.Require().True(page1.Pg.HasMore)
	page2 := s.listRows(tenant, "?search=vat&sort=name:asc&limit=1&offset=1")
	s.Require().Equal([]string{"VAT 2026 Acme"}, page2.names())
	s.Require().False(page2.Pg.HasMore)

	// --- the filters still work over the qualified columns ---
	s.Require().Equal([]string{"CIT 2026 Beta"}, s.listRows(tenant, "?status=active").sortedNames())
	s.Require().Equal(all, s.listRows(tenant, "?status=active&status=draft").Pg.Total)
	s.Require().Equal(0, s.listRows(tenant, "?status=archived").Pg.Total)
	s.Require().Equal([]string{"VAT 2025 Acme", "VAT 2026 Acme"}, s.listRows(tenant, "?entityId="+acme).sortedNames())
	s.Require().Equal(5, s.listRows(tenant, "?workflowCategory=project").Pg.Total)
	s.Require().Equal(3, s.listRows(tenant, "?workflowCategory=recurring").Pg.Total)
	s.Require().Equal([]string{"CIT 2026 Beta", "VAT 2026 Acme"}, s.listRows(tenant, "?financialYear=2026").sortedNames())
	s.Require().Equal(7, s.listRows(tenant, "?financialYear=2026&financialYear=none").Pg.Total)
	s.Require().Equal(5, s.listRows(tenant, "?financialYear=none").Pg.Total)
	// … and compose with search.
	s.Require().Equal([]string{"VAT 2026 Acme"}, s.search(tenant, "vat", "&financialYear=2026").sortedNames())
	s.Require().Equal([]string{"VAT 2026 Acme"}, s.search(tenant, "vat", "&financialYear=2026&financialYear=none").sortedNames())
	s.Require().Equal(0, s.search(tenant, "vat", "&status=active").Pg.Total)
	s.Require().Equal(2, s.search(tenant, "vat", "&status=active&status=draft&entityId="+acme).Pg.Total)
	s.Require().Equal(1, s.search(tenant, "100%", "&workflowCategory=project&financialYear=none").Pg.Total)

	// Sorting by name / createdAt, either direction, walks every row once
	// (tie-breaker w.id), and a reversed sort reverses the page.
	asc := s.listRows(tenant, "?sort=name:asc&limit=100")
	desc := s.listRows(tenant, "?sort=name:desc&limit=100")
	s.Require().Len(asc.Rows, all)
	ascNames, descNames := asc.names(), desc.names()
	for i := range ascNames {
		s.Require().Equal(ascNames[i], descNames[all-1-i])
	}
	var walked []string
	for offset := 0; offset < all; offset += 3 {
		walked = append(walked, s.listRows(tenant, "?sort=createdAt:desc&limit=3&offset="+strconv.Itoa(offset)).names()...)
	}
	s.Require().Len(walked, all)
	sort.Strings(walked)
	s.Require().Equal(base.sortedNames(), walked)

	// Over-long → 400.
	s.As(tenant).GET(s.T(), "/workflows?search="+strings.Repeat("v", 201)).AssertStatus(s.T(), http.StatusBadRequest)

	// The other tenant searches its own rows only.
	got = s.search(other, "vat", "")
	s.Require().Equal([]string{"VAT other tenant"}, got.sortedNames())
	s.Require().Equal(1, got.Pg.Total)
}
