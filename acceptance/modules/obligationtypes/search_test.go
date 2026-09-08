package obligationtypes_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The three hostile names of the scale fixture (seed/demo/scale/shape.go).
const (
	hostileAmpersand  = "Müller & Söhne 100% GmbH"
	hostileUnderscore = "Under_score Holdings Ltd"
	hostileBackslash  = `O'Brien \ Partners`
)

type codesPage struct {
	Codes []string
	Pg    struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

// listCodes GETs /obligation-types with the raw query string and returns the
// page's codes (in page order) plus the pagination block.
func (s *ObligationTypesSuite) listCodes(tenant, query string) codesPage {
	r := s.As(tenant).GET(s.T(), "/obligation-types"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status bool `json:"status"`
		Data   []struct {
			Code string `json:"code"`
		} `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := codesPage{Codes: []string{}}
	for _, ot := range env.Data {
		out.Codes = append(out.Codes, ot.Code)
	}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

func (s *ObligationTypesSuite) search(tenant, term, extra string) codesPage {
	p := s.listCodes(tenant, "?search="+url.QueryEscape(term)+extra)
	sort.Strings(p.Codes)
	return p
}

func (s *ObligationTypesSuite) create(tenant, name, code, template string) {
	s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": name, "code": code, "template": template}).
		AssertStatus(s.T(), http.StatusCreated)
}

// TestSearchNarrowsPageAndTotal: `search` is a literal, case-insensitive
// substring of name OR code; page and total narrow together; % _ \ match
// themselves; blank is no search; it composes with sort + limit + filters;
// over-long is a 400; RLS keeps another tenant's rows out.
func (s *ObligationTypesSuite) TestSearchNarrowsPageAndTotal() {
	tenant := s.InsertTenant("ot-search", "OT Search").String()
	other := s.InsertTenant("ot-search-b", "OT Search B").String()

	s.create(tenant, hostileAmpersand, "MS-100", "Custom")
	s.create(tenant, hostileUnderscore, "US-HOLD", "Custom")
	s.create(tenant, hostileBackslash, "OB-PART", "Custom")
	s.create(tenant, "VAT Return", "VAT-RET", "VAT")
	s.create(tenant, "Value Added Tax Monthly", "VAT-M", "VAT") // matched by CODE only
	s.create(tenant, "Corporate Income Tax", "CIT-A", "CIT")
	// Decoys for an unescaped `100%` / `Under_score`.
	s.create(tenant, "Room 1001 Levy", "R1001", "Custom")
	s.create(tenant, "UnderXscore Levy", "UXS", "Custom")
	s.create(other, "VAT Foreign", "VAT-F", "VAT")
	const all = 8

	// Name OR code, case-insensitive: "vat" hits VAT-RET by name and code,
	// VAT-M by code alone.
	got := s.search(tenant, "vat", "")
	s.Require().Equal([]string{"VAT-M", "VAT-RET"}, got.Codes)
	s.Require().Equal(2, got.Pg.Total)
	s.Require().Equal(got, s.search(tenant, "VAT", ""))
	got = s.search(tenant, "vat-ret", "")
	s.Require().Equal([]string{"VAT-RET"}, got.Codes)
	s.Require().Equal(1, got.Pg.Total)

	cases := map[string][]string{
		"100%":               {"MS-100"},
		"& Söhne":            {"MS-100"},
		"müller":             {"MS-100"},
		"Under_score":        {"US-HOLD"},
		`\`:                  {"OB-PART"},
		`O'Brien \ Partners`: {"OB-PART"},
		"100":                {"MS-100", "R1001"},
		"Under":              {"US-HOLD", "UXS"},
		"nobody":             {},
	}
	for term, want := range cases {
		sort.Strings(want) // search() sorts the page bytewise; compare sets
		got = s.search(tenant, term, "")
		s.Require().Equal(want, got.Codes, "search %q", term)
		s.Require().Equal(len(want), got.Pg.Total, "total for search %q", term)
	}

	// Blank search is no search.
	base := s.listCodes(tenant, "?sort=code:asc")
	s.Require().Equal(all, base.Pg.Total)
	s.Require().Len(base.Codes, all)
	s.Require().Equal(base, s.listCodes(tenant, "?sort=code:asc&search="))
	s.Require().Equal(base, s.listCodes(tenant, "?sort=code:asc&search=%20"))

	// search + sort=name:asc + limit: one row per page, the searched total.
	page1 := s.listCodes(tenant, "?search=vat&sort=name:asc&limit=1")
	s.Require().Equal([]string{"VAT-RET"}, page1.Codes) // "VAT Return" < "Value Added…"
	s.Require().Equal(2, page1.Pg.Total)
	s.Require().True(page1.Pg.HasMore)
	page2 := s.listCodes(tenant, "?search=vat&sort=name:asc&limit=1&offset=1")
	s.Require().Equal([]string{"VAT-M"}, page2.Codes)
	s.Require().False(page2.Pg.HasMore)

	// Composes with the column filters.
	s.Require().Equal(2, s.search(tenant, "vat", "&template=VAT").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "vat", "&template=CIT").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "vat", "&status=inactive").Pg.Total)

	// Over-long → 400.
	s.As(tenant).GET(s.T(), "/obligation-types?search="+strings.Repeat("x", 201)).AssertStatus(s.T(), http.StatusBadRequest)

	// Another tenant searches its own rows only.
	got = s.search(other, "vat", "")
	s.Require().Equal([]string{"VAT-F"}, got.Codes)
	s.Require().Equal(1, got.Pg.Total)
}
