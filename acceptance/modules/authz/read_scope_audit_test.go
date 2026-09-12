package authz_test

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// Read-side scope, part two: the audit trail and the member directory.
//
// The first read-scope increment narrowed the seven domain read modules and
// left the trail alone, because the only scoped audit-log assertion it had used
// a PREPARER — who is refused on capability grounds — so nothing ever put a
// scoped principal that can actually reach /audit-log in front of it. A
// reviewer can: it is the scopeable role that holds audit:read, and it is the
// role an external advisor is given. Through that hole the advisor read the
// whole group's trail — workflow names, resource ids, actors and the
// field-level before/after values — for entities the same token 404s on.
//
// The member directory is the mirror image: it is deliberately NOT narrowed (a
// scoped user needs the assignee picker), but the rows it returned carried
// entity data, not only directory data — every grant resolved the name of its
// scope entity, so a scoped caller learned the names of the siblings it cannot
// read.

type trailRow struct {
	Action       string  `json:"action"`
	ResourceType string  `json:"resourceType"`
	ResourceID   string  `json:"resourceId"`
	WorkflowID   *string `json:"workflowId"`
	WorkflowName *string `json:"workflowName"`
}

// trail reads the audit trail as the given requester: the rows and the total.
func (s *AuthzSuite) trail(rb *acceptance.RequestBuilder, query string) ([]trailRow, int) {
	r := rb.GET(s.T(), "/audit-log"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool           `json:"status"`
		Data       []trailRow     `json:"data"`
		Pagination map[string]any `json:"pagination"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env), r.BodyString())
	s.Require().True(env.Status, r.BodyString())
	total := -1
	if t, ok := env.Pagination["total"].(float64); ok {
		total = int(t)
	}
	return env.Data, total
}

func hasAction(rows []trailRow, action string) bool {
	for _, row := range rows {
		if row.Action == action {
			return true
		}
	}
	return false
}

func hasResourceType(rows []trailRow, resourceType string) bool {
	for _, row := range rows {
		if row.ResourceType == resourceType {
			return true
		}
	}
	return false
}

// TestReadScope_AuditTrail: the trail is narrowed to the caller's read scope,
// per resource family, page AND total together — while a tenant-wide grant
// keeps reading the complete ledger, tenant-level entries included.
func (s *AuthzSuite) TestReadScope_AuditTrail() {
	tenant := s.InsertTenant("rs-audit", "Read Scope Audit").String()
	f := s.seedScopeFixture(tenant)
	admin := s.As(tenant)

	// An event that references NO entity: inviting a member. It is recorded
	// against resource_type "user", which reaches no entity by any join, and it
	// is exactly the kind of entry a tenant-wide reader must keep seeing.
	var invite struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	r := admin.POST(s.T(), "/members", map[string]any{
		"email": "advisor@acme.test", "name": "Advisor", "role": "reviewer",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &invite)
	s.Require().NotEmpty(invite.Member.ID)

	// The principal: a reviewer scoped to the French subtree. Reviewer is the
	// scopeable role that HOLDS audit:read — a scoped preparer is refused at the
	// capability gate, which is why this hole outlived the first increment.
	fr := s.AsScopedRole(tenant, "reviewer", f.fr)

	adminRows, adminTotal := s.trail(admin, "?limit=500")
	rows, total := s.trail(fr, "?limit=500")
	s.Require().Equal(len(rows), total, "the total must describe the narrowed set")
	s.Require().Less(total, adminTotal, "a scoped reviewer must not read the whole tenant's trail")

	// Nothing the narrowed trail returns references anything outside the scope.
	// Asserted as the complement (no out-of-scope id may appear anywhere in the
	// body) rather than as a whitelist, so an unnarrowed resource family that is
	// added later fails here instead of slipping through.
	//
	// Two ids are deliberately NOT in this set, because a scoped reader may
	// already read them elsewhere and hiding them here would prove nothing:
	// the holding's id (the scope root's own parentEntityId is returned by
	// /entities and recorded in its entity.created envelope, so that a scoped
	// reader can tell a parent exists without reading it) and the obligation
	// type's (tenant-level reference data is never narrowed — see
	// platform/authz/readscope.go). What must not be readable is their own
	// events, which the resourceId probes below assert.
	out := map[string]string{
		"the sibling subtree":              f.de,
		"its entity obligation":            f.deEO,
		"its workflow":                     f.deWF,
		"its task template":                f.deTask,
		"its task instance":                f.deInst,
		"its document":                     f.deDoc,
		"the entity-less project workflow": f.tenantWF,
		"the invited member":               invite.Member.ID,
	}
	body := fr.GET(s.T(), "/audit-log?limit=500").BodyString()
	for what, id := range out {
		s.Require().NotContains(body, id, "the narrowed trail leaks %s", what)
	}
	s.Require().NotContains(body, "Deutschland GmbH", "the narrowed trail leaks a sibling's name")
	s.Require().NotContains(body, "DE VAT 2025", "the narrowed trail leaks a sibling's workflow name")

	// It is narrowed, not empty: every French resource family is still there.
	for _, id := range []string{f.fr, f.frEO, f.frWF, f.frTask, f.frInst, f.frDoc} {
		s.Require().Contains(body, id, "the narrowed trail dropped an in-scope resource")
	}
	s.Require().True(hasAction(rows, "workflow.started"), "actions(%v)", rows)
	s.Require().True(hasAction(rows, "document.created"))
	s.Require().True(hasAction(rows, "entity_obligation.created"))
	for _, row := range rows {
		if row.WorkflowName != nil {
			s.Require().Contains([]string{"FR VAT 2025", "FR Sub VAT 2025"}, *row.WorkflowName)
		}
	}

	// The DECISION on entries that resolve no entity: a tenant-level row is not
	// in a narrowed read scope (readscope.go — "a tenant-level resource needs a
	// tenant-wide grant, exactly as it does for writes"), so the member invite,
	// the obligation-type catalogue and the entity-less project workflow narrow
	// away for the scoped reviewer...
	s.Require().False(hasResourceType(rows, "user"), "a tenant-level member event is not in a narrowed scope")
	s.Require().False(hasResourceType(rows, "obligation_type"))
	s.Require().False(hasAction(rows, "member.invited"))

	// ...and stay fully visible to a principal holding a tenant-wide grant,
	// which is the principal a compliance audit reads the ledger as.
	s.Require().True(hasAction(adminRows, "member.invited"), "a tenant-wide reader must keep the whole ledger")
	s.Require().True(hasResourceType(adminRows, "user"))
	s.Require().True(hasResourceType(adminRows, "obligation_type"))
	_, adminProject := s.trail(admin, "?workflowId="+f.tenantWF)
	s.Require().NotZero(adminProject, "the entity-less project workflow's events must not vanish for everyone")

	// The total narrows on a partial page too: a narrowed page carrying the
	// tenant's total would report the size of what it withheld.
	page, pagedTotal := s.trail(fr, "?limit=2")
	s.Require().Len(page, 2)
	s.Require().Equal(total, pagedTotal)

	// Targeted filters cannot reach out of the scope, so none of them is an
	// existence oracle: the sibling's workflow, its instance's entity and the
	// entity-less project workflow all answer an empty, narrowed total — while
	// the same filters aimed inside the scope answer.
	for _, query := range []string{
		"?workflowId=" + f.deWF,
		"?workflowId=" + f.tenantWF,
		"?resourceType=entity&resourceId=" + f.de,
		"?resourceType=entity&resourceId=" + f.group,
		"?resourceType=entity_obligation&resourceId=" + f.deEO,
		"?resourceType=workflow_task&resourceId=" + f.deTask,
		"?resourceType=task_instance&resourceId=" + f.deInst,
		"?resourceType=obligation_type&resourceId=" + f.obType,
	} {
		hidden, hiddenTotal := s.trail(fr, query)
		s.Require().Empty(hidden, "filter reached outside the scope: %s", query)
		s.Require().Zero(hiddenTotal, "filter leaked a total outside the scope: %s", query)
	}
	for _, query := range []string{
		"?workflowId=" + f.frWF,
		"?resourceType=entity&resourceId=" + f.fr,
		"?resourceType=entity_obligation&resourceId=" + f.frEO,
		"?resourceType=task_instance&resourceId=" + f.frInst,
	} {
		visible, visibleTotal := s.trail(fr, query)
		s.Require().NotEmpty(visible, "the narrowed trail refused an in-scope filter: %s", query)
		s.Require().Equal(len(visible), visibleTotal)
	}

	// The capability gate is untouched: a role without audit:read is still 403,
	// never a 404 or an empty page (narrowing must not blur "you may not" into
	// "there is nothing").
	s.AsScopedRole(tenant, "preparer", f.fr).GET(s.T(), "/audit-log").
		AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "viewer").GET(s.T(), "/audit-log").
		AssertStatus(s.T(), http.StatusForbidden)

	// An ungranted-for-the-capability principal reads nothing rather than
	// everything — proven through the other tenant, whose ledger is empty here.
	other := s.InsertTenant("rs-audit-b", "Read Scope Audit B").String()
	otherRows, otherTotal := s.trail(s.As(other), "")
	s.Require().Empty(otherRows)
	s.Require().Zero(otherTotal)
}

type dirGrant struct {
	ID              string  `json:"id"`
	Role            string  `json:"role"`
	ScopeEntityID   *string `json:"scopeEntityId"`
	ScopeEntityName *string `json:"scopeEntityName"`
	ScopeWithheld   bool    `json:"scopeWithheld"`
}

type dirMember struct {
	ID     string     `json:"id"`
	Email  string     `json:"email"`
	Grants []dirGrant `json:"grants"`
}

func (s *AuthzSuite) directory(rb *acceptance.RequestBuilder) []dirMember {
	var members []dirMember
	r := rb.GET(s.T(), "/members?limit=100")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &members)
	return members
}

// TestReadScope_MemberDirectoryWithholdsForeignScopes: the directory stays
// unnarrowed — a scoped user needs the assignee picker — but a grant's SCOPE
// ENTITY is entity data, so it is withheld when it lies outside the caller's
// read scope. The caller's own grant and every tenant-wide grant are untouched.
func (s *AuthzSuite) TestReadScope_MemberDirectoryWithholdsForeignScopes() {
	tenant := s.InsertTenant("rs-dir", "Read Scope Directory").String()
	f := s.seedScopeFixture(tenant)
	admin := s.As(tenant)

	// Two scoped members, one per national subtree — the shape the Settings UI
	// creates when an external advisor is engaged on one entity.
	s.AsScopedRole(tenant, "viewer", f.de)
	fr := s.AsScopedRole(tenant, "reviewer", f.fr)

	// As the ADMIN (unbounded) the directory is unchanged: both scopes resolve
	// their name and their id, and nothing is marked withheld.
	var deMemberID string
	for _, m := range s.directory(admin) {
		for _, g := range m.Grants {
			s.Require().False(g.ScopeWithheld, "an unbounded caller withholds nothing")
			if g.ScopeEntityID != nil && *g.ScopeEntityID == f.de {
				deMemberID = m.ID
				s.Require().Equal("Deutschland GmbH", *g.ScopeEntityName)
			}
		}
	}
	s.Require().NotEmpty(deMemberID, "the German-scoped member must be in the directory")

	// As the France-scoped reviewer the SAME directory lists every member — the
	// picker still works — but the German grant carries no name, no id, and says
	// so; its role and the member's identity are still there.
	members := s.directory(fr)
	s.Require().Len(members, len(s.directory(admin)), "the directory itself is never narrowed")

	var sawWithheld, sawOwn, sawTenantWide bool
	for _, m := range members {
		for _, g := range m.Grants {
			switch {
			case m.ID == deMemberID:
				s.Require().True(g.ScopeWithheld, "a foreign scope must be withheld")
				s.Require().Nil(g.ScopeEntityName)
				s.Require().Nil(g.ScopeEntityID, "the id alone is still a usable join key")
				s.Require().Equal("viewer", g.Role, "the directory data itself stays")
				s.Require().NotEmpty(g.ID)
				sawWithheld = true
			case g.ScopeEntityID != nil:
				// The caller's own scope root (and its subtree) is readable.
				s.Require().False(g.ScopeWithheld)
				s.Require().Equal(f.fr, *g.ScopeEntityID)
				s.Require().Equal("France SAS", *g.ScopeEntityName)
				sawOwn = true
			default:
				// A tenant-wide grant has no scope entity to withhold, and must
				// not be confused with a withheld one.
				s.Require().False(g.ScopeWithheld, "a tenant-wide grant is not a withheld scope")
				s.Require().Nil(g.ScopeEntityName)
				sawTenantWide = true
			}
		}
	}
	s.Require().True(sawWithheld && sawOwn && sawTenantWide, "fixture must cover all three grant shapes")

	// Neither the name nor the id of the sibling reaches the wire, on the list
	// or on the single read — /members/{id} was the sharper of the two.
	for _, path := range []string{"/members?limit=100", "/members/" + deMemberID} {
		body := fr.GET(s.T(), path).BodyString()
		s.Require().NotContains(body, "Deutschland GmbH", "%s leaks a sibling's name", path)
		s.Require().NotContains(body, f.de, "%s leaks a sibling's entity id", path)
		s.Require().NotContains(body, f.group, "%s leaks the holding's id", path)
	}

	// The admin's single read is unaffected — the edit flow keeps full fidelity.
	body := admin.GET(s.T(), "/members/"+deMemberID).BodyString()
	s.Require().Contains(body, "Deutschland GmbH")
	s.Require().Contains(body, f.de)
	s.Require().True(strings.Contains(body, `"scopeWithheld":false`), body)
}
