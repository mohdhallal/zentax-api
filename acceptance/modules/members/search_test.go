package members_test

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

type membersPage struct {
	Names []string
	Pg    struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

func (s *MembersSuite) listMembers(tenant, query string) membersPage {
	r := s.As(tenant).GET(s.T(), "/members"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool            `json:"status"`
		Data       []member        `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := membersPage{Names: []string{}}
	for _, m := range env.Data {
		out.Names = append(out.Names, m.Name)
	}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

func (s *MembersSuite) search(tenant, term, extra string) membersPage {
	p := s.listMembers(tenant, "?search="+url.QueryEscape(term)+extra)
	sort.Strings(p.Names)
	return p
}

// TestSearchNarrowsPageAndTotal: `search` on the directory is a literal,
// case-insensitive substring of name OR email; page and total narrow
// together; % _ \ match themselves; blank is no search; it composes with
// kind / status / limit; over-long is a 400; a tenant only ever searches its
// own users (users is not RLS-scoped — the predicate is explicit).
func (s *MembersSuite) TestSearchNarrowsPageAndTotal() {
	tenant := s.InsertTenant("mb-search", "Members Search").String()
	other := s.InsertTenant("mb-search-b", "Members Search B").String()
	// The seeded tenant_admin ("Test User", user-…@test.local) exists from
	// the first As() call; count it in.
	s.As(tenant).GET(s.T(), "/members").AssertStatus(s.T(), http.StatusOK)

	s.invite(tenant, "mueller@acme.com", hostileAmpersand, "viewer")
	s.invite(tenant, "underscore@acme.com", hostileUnderscore, "viewer")
	s.invite(tenant, "obrien@acme.com", hostileBackslash, "viewer")
	s.invite(tenant, "room1001@acme.com", "Room 1001 Ltd", "viewer")
	s.invite(tenant, "underx@acme.com", "UnderXscore Ltd", "viewer")
	s.invite(tenant, "jane@example.org", "Jane Doe", "preparer")
	s.invite(other, "foreign@acme.com", "Acme Foreign", "viewer")
	const all = 7 // six invited + the seeded admin

	// By email: the five @acme.com invitees; case-insensitive.
	acme := []string{hostileAmpersand, hostileBackslash, "Room 1001 Ltd", hostileUnderscore, "UnderXscore Ltd"}
	sort.Strings(acme)
	got := s.search(tenant, "acme", "")
	s.Require().Equal(acme, got.Names)
	s.Require().Equal(5, got.Pg.Total)
	s.Require().Equal(got, s.search(tenant, "ACME.COM", ""))

	// By name, and by the other email's domain.
	got = s.search(tenant, "jane", "")
	s.Require().Equal([]string{"Jane Doe"}, got.Names)
	s.Require().Equal(1, got.Pg.Total)
	got = s.search(tenant, "example.org", "")
	s.Require().Equal([]string{"Jane Doe"}, got.Names)
	s.Require().Equal(1, got.Pg.Total)

	cases := map[string][]string{
		"100%":               {hostileAmpersand},
		"& Söhne":            {hostileAmpersand},
		"müller":             {hostileAmpersand},
		"Under_score":        {hostileUnderscore},
		`\`:                  {hostileBackslash},
		`O'Brien \ Partners`: {hostileBackslash},
		"100":                {hostileAmpersand, "Room 1001 Ltd"},
		"Under":              {hostileUnderscore, "UnderXscore Ltd"},
		"nobody":             {},
	}
	for term, want := range cases {
		sort.Strings(want) // search() sorts the page bytewise; compare sets
		got = s.search(tenant, term, "")
		s.Require().Equal(want, got.Names, "search %q", term)
		s.Require().Equal(len(want), got.Pg.Total, "total for search %q", term)
	}

	// Blank search is no search.
	base := s.listMembers(tenant, "?kind=human")
	s.Require().Equal(all, base.Pg.Total)
	s.Require().Len(base.Names, all)
	s.Require().Equal(base, s.listMembers(tenant, "?kind=human&search="))
	s.Require().Equal(base, s.listMembers(tenant, "?kind=human&search=%20%20"))

	// search + limit: the searched total and hasMore, page by page, with the
	// pages disjoint and complete (ORDER BY name, email, id).
	var walked []string
	for offset := 0; ; offset += 2 {
		p := s.listMembers(tenant, "?search=acme&limit=2&offset="+strconv.Itoa(offset))
		s.Require().Equal(5, p.Pg.Total)
		s.Require().Equal(2, p.Pg.Limit)
		s.Require().Equal(offset+len(p.Names) < 5, p.Pg.HasMore)
		walked = append(walked, p.Names...)
		if !p.Pg.HasMore {
			break
		}
		s.Require().Len(p.Names, 2)
	}
	sort.Strings(walked)
	s.Require().Equal(acme, walked)

	// Composes with kind and status.
	s.Require().Equal(5, s.search(tenant, "acme", "&kind=human&status=invited").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "acme", "&status=active").Pg.Total)
	s.Require().Equal(0, s.search(tenant, "acme", "&kind=service").Pg.Total)

	// Over-long → 400.
	s.As(tenant).GET(s.T(), "/members?search="+strings.Repeat("m", 201)).AssertStatus(s.T(), http.StatusBadRequest)

	// The other tenant finds only its own user, never this tenant's.
	got = s.search(other, "acme", "")
	s.Require().Equal([]string{"Acme Foreign"}, got.Names)
	s.Require().Equal(1, got.Pg.Total)
}
