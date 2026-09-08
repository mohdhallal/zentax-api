package entities_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The three hostile names of the scale fixture (seed/demo/scale/shape.go):
// each carries a LIKE metacharacter or the escape character, so a search
// that does not escape its term matches too much (or errors).
const (
	hostileAmpersand  = "Müller & Söhne 100% GmbH"
	hostileUnderscore = "Under_score Holdings Ltd"
	hostileBackslash  = `O'Brien \ Partners`
)

type namesPage struct {
	Names []string
	Pg    struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

// listNames GETs /entities with the raw query string and returns the page's
// names (in page order) plus the pagination block.
func (s *EntitiesSuite) listNames(tenant, query string) namesPage {
	r := s.As(tenant).GET(s.T(), "/entities"+query)
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
	out := namesPage{Names: []string{}}
	for _, e := range env.Data {
		out.Names = append(out.Names, e.Name)
	}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

// search runs one search (URL-encoded) with extra query params appended and
// returns the sorted names, so assertions are collation-independent.
func (s *EntitiesSuite) search(tenant, term, extra string) namesPage {
	p := s.listNames(tenant, "?search="+url.QueryEscape(term)+extra)
	sort.Strings(p.Names)
	return p
}

func (s *EntitiesSuite) createNamed(tenant, name string, legalName *string) string {
	body := map[string]any{"name": name, "country": "Germany"}
	if legalName != nil {
		body["legalName"] = *legalName
	}
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func str(v string) *string { return &v }

// TestSearchNarrowsPageAndTotal: `search` is a literal, case-insensitive
// substring of name OR legal_name. The page and the total narrow together;
// % _ and \ match themselves (the decoys differ from the hostile names by
// exactly what an unescaped metacharacter would swallow); blank is no search;
// it composes with sort + limit + filters; an over-long term is a 400; RLS
// keeps another tenant's rows out.
func (s *EntitiesSuite) TestSearchNarrowsPageAndTotal() {
	tenant := s.InsertTenant("ent-search", "Entities Search").String()
	other := s.InsertTenant("ent-search-b", "Entities Search B").String()

	s.createNamed(tenant, hostileAmpersand, str("Müller und Söhne Gesellschaft"))
	s.createNamed(tenant, hostileUnderscore, nil)
	s.createNamed(tenant, hostileBackslash, nil)
	s.createNamed(tenant, "Acme GmbH", str("Acme Aktiengesellschaft"))
	s.createNamed(tenant, "Acme Sub", str("Zeta Legal Holdings"))
	s.createNamed(tenant, "Plain Co", nil)
	// Decoys: matched by an UNESCAPED `100%` / `Under_score`, never by the
	// literal terms.
	s.createNamed(tenant, "Room 1001 Ltd", nil)
	s.createNamed(tenant, "UnderXscore Ltd", nil)
	s.createNamed(other, "Acme Foreign", nil)
	const all = 8

	// Narrows page AND total; case-insensitive.
	got := s.search(tenant, "acme", "")
	s.Require().Equal([]string{"Acme GmbH", "Acme Sub"}, got.Names)
	s.Require().Equal(2, got.Pg.Total)
	s.Require().Equal(got, s.search(tenant, "ACME", ""))

	// legal_name is searched too.
	got = s.search(tenant, "zeta", "")
	s.Require().Equal([]string{"Acme Sub"}, got.Names)
	s.Require().Equal(1, got.Pg.Total)

	// The hostile names, by the substrings a user would type.
	cases := map[string][]string{
		"100%":               {hostileAmpersand},
		"& Söhne":            {hostileAmpersand},
		"müller":             {hostileAmpersand},
		"Under_score":        {hostileUnderscore},
		"_score":             {hostileUnderscore},
		`\`:                  {hostileBackslash},
		`O'Brien \ Partners`: {hostileBackslash},
		"'":                  {hostileBackslash},
		"100":                {hostileAmpersand, "Room 1001 Ltd"},
		"Under":              {hostileUnderscore, "UnderXscore Ltd"},
		"nobody":             {},
	}
	for term, want := range cases {
		sort.Strings(want) // search() sorts the page bytewise; compare sets
		got = s.search(tenant, term, "")
		s.Require().Equal(want, got.Names, "search %q", term)
		s.Require().Equal(len(want), got.Pg.Total, "total for search %q", term)
		s.Require().False(got.Pg.HasMore)
	}

	// Blank search is no search — same page and total as no parameter.
	base := s.listNames(tenant, "?sort=name:asc")
	s.Require().Equal(all, base.Pg.Total)
	s.Require().Len(base.Names, all)
	s.Require().Equal(base, s.listNames(tenant, "?sort=name:asc&search="))
	s.Require().Equal(base, s.listNames(tenant, "?sort=name:asc&search=%20%20"))

	// search + sort + limit: the two Acme rows, one per page, name ASC, with
	// the total and hasMore describing the SEARCHED set.
	page1 := s.listNames(tenant, "?search=acme&sort=name:asc&limit=1")
	s.Require().Equal([]string{"Acme GmbH"}, page1.Names)
	s.Require().Equal(2, page1.Pg.Total)
	s.Require().True(page1.Pg.HasMore)
	page2 := s.listNames(tenant, "?search=acme&sort=name:asc&limit=1&offset=1")
	s.Require().Equal([]string{"Acme Sub"}, page2.Names)
	s.Require().Equal(2, page2.Pg.Total)
	s.Require().False(page2.Pg.HasMore)

	// Composes with the column filters (same WHERE in page and count).
	s.Require().Equal(2, s.search(tenant, "acme", "&status=active").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "acme", "&status=archived").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "acme", "&country=France").Pg.Total)

	// Over-long → 400; exactly the cap is fine.
	s.As(tenant).GET(s.T(), "/entities?search="+strings.Repeat("a", 201)).AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/entities?search="+strings.Repeat("a", 200)).AssertStatus(s.T(), http.StatusOK)

	// Another tenant searches its own rows only.
	got = s.search(other, "acme", "")
	s.Require().Equal([]string{"Acme Foreign"}, got.Names)
	s.Require().Equal(1, got.Pg.Total)
}
