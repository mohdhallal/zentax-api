package entities_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

type entityPage struct {
	Body string
	IDs  []string
	Pg   struct {
		Total   int  `json:"total"`
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"hasMore"`
	}
}

func (s *EntitiesSuite) entityPage(tenant, path string) entityPage {
	r := s.As(tenant).GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status bool `json:"status"`
		Data   []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	body := r.BodyString()
	s.Require().NoError(json.Unmarshal([]byte(body), &env), body)
	s.Require().True(env.Status, body)
	out := entityPage{Body: body}
	for _, e := range env.Data {
		s.Require().Equal("Same Name Ltd", e.Name)
		out.IDs = append(out.IDs, e.ID)
	}
	s.Require().NoError(json.Unmarshal(env.Pagination, &out.Pg), body)
	return out
}

// TestNameSortTieBreakWalk: ten entities with the same name, sorted by that
// name three per page — a full tie on the sort column. The id tie-breaker
// makes the four pages disjoint and complete (no duplicate, no gap), in id
// order within the tie, and identical on a second walk.
func (s *EntitiesSuite) TestNameSortTieBreakWalk() {
	tenant := s.InsertTenant("ent-pg", "Entities Paging").String()

	created := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		var e struct {
			ID string `json:"id"`
		}
		r := s.As(tenant).POST(s.T(), "/entities", map[string]any{
			"name": "Same Name Ltd", "country": "Germany", "legalName": fmt.Sprintf("Same Name %d", i),
		})
		r.AssertStatus(s.T(), http.StatusCreated)
		r.DecodeData(s.T(), &e)
		created = append(created, e.ID)
	}
	sort.Strings(created)

	walk := func() []entityPage {
		var pages []entityPage
		for offset := 0; ; offset += 3 {
			p := s.entityPage(tenant, fmt.Sprintf("/entities?sort=name:asc&limit=3&offset=%d", offset))
			s.Require().Equal(10, p.Pg.Total)
			s.Require().Equal(3, p.Pg.Limit)
			s.Require().Equal(offset, p.Pg.Offset)
			s.Require().Equal(offset+len(p.IDs) < 10, p.Pg.HasMore)
			pages = append(pages, p)
			if !p.Pg.HasMore {
				break
			}
			s.Require().Len(p.IDs, 3)
		}
		return pages
	}

	first := walk()
	s.Require().Len(first, 4)
	s.Require().Len(first[3].IDs, 1)

	var ids []string
	for _, p := range first {
		ids = append(ids, p.IDs...)
	}
	s.Require().Len(ids, 10)
	// name ASC ties → id ASC: the concatenation is exactly the sorted id set,
	// which also proves no duplicate and no gap.
	s.Require().Equal(created, ids)

	second := walk()
	for i := range first {
		s.Require().Equal(first[i].Body, second[i].Body, "page %d differs between two identical walks", i)
	}
}
