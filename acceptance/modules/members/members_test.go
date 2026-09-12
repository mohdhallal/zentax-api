package members_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// MembersSuite proves member administration end to end against live Postgres:
// invite (one-time "zti_" token) → accept (password, activation, audit in the
// token's tenant) → login; the directory is readable by every role and
// mutable only by tenant admins; disable revokes sessions; the last-admin
// guard; grant add/remove; cross-tenant 404s; task assignment validation.
type MembersSuite struct {
	acceptance.Suite
}

func TestMembersSuite(t *testing.T) {
	suite.Run(t, new(MembersSuite))
}

const cookieName = "zentax_session"

func sessionCookie(resp *acceptance.TestResponse) string {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}

type member struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Email      string  `json:"email"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	MFAEnabled bool    `json:"mfaEnabled"`
	Grants     []grant `json:"grants"`
	CreatedAt  string  `json:"createdAt"`
}

type grant struct {
	ID              string  `json:"id"`
	Role            string  `json:"role"`
	ScopeEntityID   *string `json:"scopeEntityId"`
	ScopeEntityName *string `json:"scopeEntityName"`
}

type invite struct {
	Member          *member `json:"member"`
	InviteToken     string  `json:"inviteToken"`
	InviteExpiresAt string  `json:"inviteExpiresAt"`
}

func (s *MembersSuite) invite(tenant, email, name, role string) invite {
	var inv invite
	r := s.As(tenant).POST(s.T(), "/members", map[string]any{"email": email, "name": name, "role": role})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &inv)
	s.Require().True(strings.HasPrefix(inv.InviteToken, "zti_"), "invite token: %s", inv.InviteToken)
	s.Require().NotEmpty(inv.InviteExpiresAt)
	s.Require().NotNil(inv.Member)
	return inv
}

func (s *MembersSuite) accept(token, password string) *acceptance.TestResponse {
	return s.Client.External().POST(s.T(), "/auth/accept-invite", map[string]any{"token": token, "password": password})
}

func (s *MembersSuite) login(email, password string) *acceptance.TestResponse {
	return s.Client.External().POST(s.T(), "/auth/login", map[string]any{"email": email, "password": password})
}

// inviteAndActivate runs the whole invite → accept → login flow and returns
// the new member's id and a live session builder.
func (s *MembersSuite) inviteAndActivate(tenant, email, name, role string) (string, *acceptance.RequestBuilder) {
	inv := s.invite(tenant, email, name, role)
	s.accept(inv.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)
	login := s.login(email, "correct-horse-battery-staple")
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)
	s.Require().NotEmpty(token)
	return inv.Member.ID, s.Client.External().WithSession(token)
}

func (s *MembersSuite) me(rb *acceptance.RequestBuilder) string {
	var me struct {
		ID string `json:"id"`
	}
	rb.GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	return me.ID
}

func (s *MembersSuite) getMember(tenant, id string) member {
	var m member
	r := s.As(tenant).GET(s.T(), "/members/"+id)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &m)
	return m
}

func (s *MembersSuite) TestInviteAcceptLogin() {
	tenant := s.InsertTenant("mb-a", "Members Tenant A").String()

	inv := s.invite(tenant, "Jane.Doe@Acme.com", "Jane Doe", "preparer")
	s.Require().Equal("jane.doe@acme.com", inv.Member.Email, "stored lowercased")
	s.Require().Equal("invited", inv.Member.Status)
	s.Require().Equal("human", inv.Member.Kind)
	s.Require().Len(inv.Member.Grants, 1)
	s.Require().Equal("preparer", inv.Member.Grants[0].Role)
	s.Require().Nil(inv.Member.Grants[0].ScopeEntityID)

	// Listed with status invited + grant; the token is never listed.
	var list []member
	r := s.As(tenant).GET(s.T(), "/members?status=invited")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &list)
	s.Require().Len(list, 1)
	s.Require().Equal(inv.Member.ID, list[0].ID)
	s.Require().NotContains(r.BodyString(), "zti_")

	// Duplicate email → 409.
	s.As(tenant).POST(s.T(), "/members", map[string]any{"email": "JANE.DOE@acme.com", "name": "Dup", "role": "viewer"}).
		AssertStatus(s.T(), http.StatusConflict)

	// An invited member cannot log in yet (no password) and cannot be
	// activated through PUT — only by accepting the invite.
	s.login("jane.doe@acme.com", "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusUnauthorized)
	s.As(tenant).PUT(s.T(), "/members/"+inv.Member.ID, map[string]any{"name": "Jane", "status": "active"}).
		AssertStatus(s.T(), http.StatusBadRequest)

	// Short password / wrong token → 400 (generic message, never a reason).
	s.accept(inv.InviteToken, "short").AssertStatus(s.T(), http.StatusBadRequest)
	wrong := s.accept("zti_definitely-not-a-real-token", "correct-horse-battery-staple")
	wrong.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(wrong.BodyString(), "invite is invalid or has expired")

	// Accept → 200 {email}; the same token again → 400 (single use).
	ok := s.accept(inv.InviteToken, "correct-horse-battery-staple")
	ok.AssertStatus(s.T(), http.StatusOK)
	var accepted struct {
		Email string `json:"email"`
	}
	ok.DecodeData(s.T(), &accepted)
	s.Require().Equal("jane.doe@acme.com", accepted.Email)
	s.accept(inv.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusBadRequest)

	// The new member logs in through the real login and sees the tenant.
	login := s.login("jane.doe@acme.com", "correct-horse-battery-staple")
	login.AssertStatus(s.T(), http.StatusOK)
	jane := s.Client.External().WithSession(sessionCookie(login))
	var me struct {
		ID       string `json:"id"`
		TenantID string `json:"tenantId"`
		Status   string `json:"status"`
	}
	jane.GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.Require().Equal(inv.Member.ID, me.ID)
	s.Require().Equal(tenant, me.TenantID)
	s.Require().Equal("active", me.Status)
	// …and holds exactly the granted role: a preparer reads the directory but
	// cannot invite; the member view shows the grant.
	jane.GET(s.T(), "/members").AssertStatus(s.T(), http.StatusOK)
	jane.POST(s.T(), "/members", map[string]any{"email": "x@acme.com", "name": "X", "role": "viewer"}).
		AssertStatus(s.T(), http.StatusForbidden)
	activated := s.getMember(tenant, inv.Member.ID)
	s.Require().Equal("active", activated.Status)
	s.Require().Len(activated.Grants, 1)

	// Re-issuing for an ACTIVE member is a 409.
	s.As(tenant).POST(s.T(), "/members/"+inv.Member.ID+"/invite", nil).AssertStatus(s.T(), http.StatusConflict)

	// Audit: invited + activated landed in THIS tenant's chain, PII-free.
	var audit []struct {
		Action     string         `json:"action"`
		ResourceID string         `json:"resourceId"`
		Details    map[string]any `json:"details"`
	}
	s.As(tenant).GET(s.T(), "/audit-log?resourceId="+inv.Member.ID).DecodeData(s.T(), &audit)
	actions := map[string]map[string]any{}
	for _, a := range audit {
		actions[a.Action] = a.Details
	}
	s.Require().Contains(actions, "member.invited")
	s.Require().Contains(actions, "member.activated")
	s.Require().Equal("preparer", actions["member.invited"]["role"])
	s.Require().Equal(false, actions["member.invited"]["scoped"])
	s.Require().NotContains(actions["member.invited"], "email")
	s.Require().NotContains(actions["member.invited"], "name")
}

func (s *MembersSuite) TestReissueRotatesToken() {
	tenant := s.InsertTenant("mb-b", "Members Tenant B").String()
	inv := s.invite(tenant, "bob@acme.com", "Bob", "viewer")

	var re invite
	r := s.As(tenant).POST(s.T(), "/members/"+inv.Member.ID+"/invite", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &re)
	s.Require().True(strings.HasPrefix(re.InviteToken, "zti_"))
	s.Require().NotEqual(inv.InviteToken, re.InviteToken)

	// The OLD token stops working; the new one activates.
	s.accept(inv.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusBadRequest)
	s.accept(re.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)
	s.login("bob@acme.com", "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)

	// Expired invite → 400: push a fresh invite past its expiry in the DB.
	exp := s.invite(tenant, "late@acme.com", "Late", "viewer")
	_, err := s.DB.Exec(`UPDATE invite_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE user_id = $1`, exp.Member.ID)
	s.Require().NoError(err)
	s.accept(exp.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusBadRequest)
}

// Every role reads the directory; only tenant_admin mutates it.
func (s *MembersSuite) TestDirectoryVisibleToAllRolesMutableByAdminsOnly() {
	tenant := s.InsertTenant("mb-c", "Members Tenant C").String()
	body := map[string]any{"email": "new@acme.com", "name": "New", "role": "viewer"}

	for _, role := range []string{"viewer", "preparer", "reviewer", "manager"} {
		s.AsRole(tenant, role).GET(s.T(), "/members").AssertStatus(s.T(), http.StatusOK)
		s.AsRole(tenant, role).POST(s.T(), "/members", body).AssertStatus(s.T(), http.StatusForbidden)
	}
	s.AsUngranted(tenant).GET(s.T(), "/members").AssertStatus(s.T(), http.StatusForbidden)
	s.Client.External().GET(s.T(), "/members").AssertStatus(s.T(), http.StatusUnauthorized)

	// The directory lists every seeded role user (names/emails/roles), paginated.
	var list []member
	r := s.As(tenant).GET(s.T(), "/members?kind=human")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &list)
	s.Require().Len(list, 6) // 4 roles + ungranted + the admin
	var pageEnv struct {
		Pagination struct {
			Total int `json:"total"`
			Limit int `json:"limit"`
		} `json:"pagination"`
	}
	s.Require().NoError(jsonUnmarshal(r.BodyString(), &pageEnv))
	s.Require().Equal(6, pageEnv.Pagination.Total)
	s.Require().Equal(100, pageEnv.Pagination.Limit)

	// The other tenant sees none of them, and its admin gets 404 on their ids.
	other := s.InsertTenant("mb-c2", "Members Tenant C2").String()
	var otherList []member
	s.As(other).GET(s.T(), "/members").DecodeData(s.T(), &otherList)
	s.Require().Len(otherList, 1) // only its own seeded admin
	s.As(other).GET(s.T(), "/members/"+list[0].ID).AssertStatus(s.T(), http.StatusNotFound)
	s.As(other).PUT(s.T(), "/members/"+list[0].ID, map[string]any{"name": "Hijack", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusNotFound)
	s.As(other).POST(s.T(), "/members/"+list[0].ID+"/invite", nil).AssertStatus(s.T(), http.StatusNotFound)
}

func (s *MembersSuite) TestScopedInviteRequiresOwnEntity() {
	tenant := s.InsertTenant("mb-d", "Members Tenant D").String()
	other := s.InsertTenant("mb-d2", "Members Tenant D2").String()

	var mine, theirs struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", map[string]any{"name": "Acme DE", "country": "Germany"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &mine)
	r = s.As(other).POST(s.T(), "/entities", map[string]any{"name": "Foreign", "country": "France"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &theirs)

	// Another tenant's entity → 400 (composite FK); the invite is not created.
	s.As(tenant).POST(s.T(), "/members", map[string]any{
		"email": "scoped@acme.com", "name": "Scoped", "role": "preparer", "scopeEntityId": theirs.ID,
	}).AssertStatus(s.T(), http.StatusBadRequest)
	var list []member
	s.As(tenant).GET(s.T(), "/members?status=invited").DecodeData(s.T(), &list)
	s.Require().Empty(list)

	// Own entity → 201, scope name resolved.
	var inv invite
	r = s.As(tenant).POST(s.T(), "/members", map[string]any{
		"email": "scoped@acme.com", "name": "Scoped", "role": "preparer", "scopeEntityId": mine.ID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &inv)
	s.Require().Len(inv.Member.Grants, 1)
	s.Require().Equal(mine.ID, *inv.Member.Grants[0].ScopeEntityID)
	s.Require().Equal("Acme DE", *inv.Member.Grants[0].ScopeEntityName)
}

func (s *MembersSuite) TestDisableRevokesAccess() {
	tenant := s.InsertTenant("mb-e", "Members Tenant E").String()
	adminID := s.me(s.As(tenant))

	memberID, memberSession := s.inviteAndActivate(tenant, "carol@acme.com", "Carol", "manager")
	memberSession.GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)

	// Disabling self → 400.
	s.As(tenant).PUT(s.T(), "/members/"+adminID, map[string]any{"name": "Admin", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusBadRequest)

	// Disabling another member → 200; sessions revoked; login refused.
	var updated member
	r := s.As(tenant).PUT(s.T(), "/members/"+memberID, map[string]any{"name": "Carol Disabled", "status": "disabled"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Equal("disabled", updated.Status)
	s.Require().Equal("Carol Disabled", updated.Name)
	memberSession.GET(s.T(), "/auth/me").AssertStatus(s.T(), http.StatusUnauthorized)
	s.login("carol@acme.com", "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusUnauthorized)
	var live int
	s.Require().NoError(s.DB.Get(&live, `SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, memberID))
	s.Require().Zero(live)

	// Re-enable → login works again.
	s.As(tenant).PUT(s.T(), "/members/"+memberID, map[string]any{"name": "Carol", "status": "active"}).
		AssertStatus(s.T(), http.StatusOK)
	s.login("carol@acme.com", "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)

	// Audit carries the status transition, both sides, and the rename dated but
	// withheld — the name is personal data (ADR-0007/0008).
	var audit []struct {
		Action  string `json:"action"`
		Details struct {
			Fields map[string]struct {
				From any `json:"from"`
				To   any `json:"to"`
			} `json:"fields"`
		} `json:"details"`
	}
	resp := s.As(tenant).GET(s.T(), "/audit-log?action=member.updated&resourceId="+memberID)
	resp.DecodeData(s.T(), &audit)
	s.Require().Len(audit, 2)
	// Newest first: [0] is the re-enable, [1] the disable.
	s.Require().Equal("disabled", audit[0].Details.Fields["status"].From)
	s.Require().Equal("active", audit[0].Details.Fields["status"].To)
	s.Require().Equal("active", audit[1].Details.Fields["status"].From)
	s.Require().Equal("disabled", audit[1].Details.Fields["status"].To)
	// Both PUTs renamed the member; the trail dates that without quoting it.
	s.Require().Equal("set", audit[1].Details.Fields["name"].From)
	s.Require().Equal("set", audit[1].Details.Fields["name"].To)
	s.Require().NotContains(resp.BodyString(), "Carol Disabled")
}

func (s *MembersSuite) TestRolesGrantsAndLastAdminGuard() {
	tenant := s.InsertTenant("mb-f", "Members Tenant F").String()
	adminID := s.me(s.As(tenant))

	// The ONLY tenant admin cannot demote itself (409), nor drop its admin grant.
	s.As(tenant).PUT(s.T(), "/members/"+adminID+"/role", map[string]any{"role": "viewer"}).
		AssertStatus(s.T(), http.StatusConflict)
	admin := s.getMember(tenant, adminID)
	s.Require().Len(admin.Grants, 1)
	s.Require().Equal("tenant_admin", admin.Grants[0].Role)
	s.As(tenant).DELETE(s.T(), "/members/"+adminID+"/grants/"+admin.Grants[0].ID).
		AssertStatus(s.T(), http.StatusConflict)
	// …and nothing changed (the guard rolled the request back).
	s.Require().Equal("tenant_admin", s.getMember(tenant, adminID).Grants[0].Role)

	// A second admin: activated through the real invite flow, so the guard
	// counts it (active + human + tenant-wide tenant_admin).
	secondID, second := s.inviteAndActivate(tenant, "dave@acme.com", "Dave", "tenant_admin")
	second.POST(s.T(), "/members", map[string]any{"email": "e@acme.com", "name": "E", "role": "viewer"}).
		AssertStatus(s.T(), http.StatusCreated)

	// Now the first admin can be demoted (replace-all): exactly one viewer grant.
	var demoted member
	r := s.As(tenant).PUT(s.T(), "/members/"+adminID+"/role", map[string]any{"role": "viewer"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &demoted)
	s.Require().Len(demoted.Grants, 1)
	s.Require().Equal("viewer", demoted.Grants[0].Role)
	// The demotion takes effect on the next request: the first admin is a viewer now.
	s.As(tenant).POST(s.T(), "/members", map[string]any{"email": "f@acme.com", "name": "F", "role": "viewer"}).
		AssertStatus(s.T(), http.StatusForbidden)

	// Grants: add a scoped reviewer grant to the second admin (201, 2 grants),
	// then remove it (204). Removing the sole admin grant → 409.
	var entity struct {
		ID string `json:"id"`
	}
	r = second.POST(s.T(), "/entities", map[string]any{"name": "Acme FR", "country": "France"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	var withGrant member
	r = second.POST(s.T(), "/members/"+secondID+"/grants", map[string]any{"role": "reviewer", "scopeEntityId": entity.ID})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &withGrant)
	s.Require().Len(withGrant.Grants, 2)
	var scoped, adminGrant *grant
	for i := range withGrant.Grants {
		if withGrant.Grants[i].Role == "reviewer" {
			scoped = &withGrant.Grants[i]
		} else {
			adminGrant = &withGrant.Grants[i]
		}
	}
	s.Require().NotNil(scoped)
	s.Require().NotNil(adminGrant)
	s.Require().Equal("Acme FR", *scoped.ScopeEntityName)

	second.DELETE(s.T(), "/members/"+secondID+"/grants/"+adminGrant.ID).AssertStatus(s.T(), http.StatusConflict)
	second.DELETE(s.T(), "/members/"+secondID+"/grants/"+scoped.ID).AssertStatus(s.T(), http.StatusNoContent)
	second.DELETE(s.T(), "/members/"+secondID+"/grants/"+scoped.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.Require().Len(s.getMember(tenant, secondID).Grants, 1)

	// Audit: a grant change carries the whole grant set on BOTH sides — role and
	// scope entity id, never a scope name (ADR-0008). The add and the remove are
	// exact mirrors, which is what makes an access review reconstructable.
	var audit []struct {
		Action  string `json:"action"`
		Details struct {
			Fields struct {
				Grants struct {
					From []map[string]any `json:"from"`
					To   []map[string]any `json:"to"`
				} `json:"grants"`
			} `json:"fields"`
		} `json:"details"`
	}
	second.GET(s.T(), "/audit-log?action=member.role_changed&resourceId="+secondID).DecodeData(s.T(), &audit)
	s.Require().Len(audit, 2)

	adminOnly := []map[string]any{{"role": "tenant_admin"}}
	adminPlusScoped := []map[string]any{
		{"role": "reviewer", "scopeEntityId": entity.ID},
		{"role": "tenant_admin"},
	}
	added, removed := audit[1].Details.Fields.Grants, audit[0].Details.Fields.Grants
	s.Require().Equal(adminOnly, added.From)
	s.Require().Equal(adminPlusScoped, added.To)
	s.Require().Equal(adminPlusScoped, removed.From)
	s.Require().Equal(adminOnly, removed.To)
}

// A machine principal never holds member:manage — neither at creation nor by
// a later grant (no self-replication, ADR-0012).
func (s *MembersSuite) TestServiceAccountCannotBeTenantAdmin() {
	tenant := s.InsertTenant("mb-g", "Members Tenant G").String()

	s.As(tenant).POST(s.T(), "/service-accounts", map[string]any{"name": "rogue", "role": "tenant_admin"}).
		AssertStatus(s.T(), http.StatusBadRequest)

	var sa struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/service-accounts", map[string]any{"name": "agent", "role": "manager"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &sa)

	s.As(tenant).PUT(s.T(), "/members/"+sa.ID+"/role", map[string]any{"role": "tenant_admin"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).POST(s.T(), "/members/"+sa.ID+"/grants", map[string]any{"role": "tenant_admin"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	// A service account is listed under kind=service and cannot be re-invited.
	var list []member
	s.As(tenant).GET(s.T(), "/members?kind=service").DecodeData(s.T(), &list)
	s.Require().Len(list, 1)
	s.Require().Equal("service", list[0].Kind)
	s.As(tenant).POST(s.T(), "/members/"+sa.ID+"/invite", nil).AssertStatus(s.T(), http.StatusConflict)
}

// Task assignment: assigneeId must be an ACTIVE HUMAN member of the caller's
// tenant, and the enriched report resolves its name.
func (s *MembersSuite) TestTaskAssignmentValidatesAgainstDirectory() {
	tenant := s.InsertTenant("mb-h", "Members Tenant H")
	other := s.InsertTenant("mb-h2", "Members Tenant H2")
	foreignUser := s.InsertUserWithPassword(other, "foreign@other.test", "correct-horse-battery-staple")

	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant.String()).POST(s.T(), path, body)
	}
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{"name": "Acme", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)
	r = post("/obligation-types", map[string]any{"name": "VAT", "code": "VAT-M", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)
	r = post("/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)
	post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)
	post("/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var instances []struct {
		ID string `json:"id"`
	}
	s.As(tenant.String()).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	taskID := instances[0].ID
	assign := func(userID string) *acceptance.TestResponse {
		return s.As(tenant.String()).PUT(s.T(), "/task-instances/"+taskID, map[string]any{"status": "in_progress", "assigneeId": userID})
	}

	// Foreign user → 400; invited (not yet active) → 400; service account → 400.
	bad := assign(foreignUser.String())
	bad.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(bad.BodyString(), "assignee is not an active member of this tenant")
	inv := s.invite(tenant.String(), "erin@acme.com", "Erin Example", "preparer")
	assign(inv.Member.ID).AssertStatus(s.T(), http.StatusBadRequest)
	var sa struct {
		ID string `json:"id"`
	}
	r = post("/service-accounts", map[string]any{"name": "bot", "role": "preparer"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &sa)
	assign(sa.ID).AssertStatus(s.T(), http.StatusBadRequest)

	// Once activated, the member is assignable and the report shows the name.
	s.accept(inv.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)
	assign(inv.Member.ID).AssertStatus(s.T(), http.StatusOK)
	var rows []struct {
		AssigneeID   *string `json:"assigneeId"`
		AssigneeName *string `json:"assigneeName"`
	}
	s.As(tenant.String()).GET(s.T(), "/reports/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &rows)
	s.Require().Len(rows, 1)
	s.Require().Equal(inv.Member.ID, *rows[0].AssigneeID)
	s.Require().NotNil(rows[0].AssigneeName)
	s.Require().Equal("Erin Example", *rows[0].AssigneeName)

	// Disabled → the task stays editable with its current assignee (clients
	// send it back on every save)…
	s.As(tenant.String()).PUT(s.T(), "/members/"+inv.Member.ID, map[string]any{"name": "Erin Example", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusOK)
	assign(inv.Member.ID).AssertStatus(s.T(), http.StatusOK)
	// …but once moved to an active member, the disabled one is not assignable again.
	assign(s.me(s.As(tenant.String()))).AssertStatus(s.T(), http.StatusOK)
	assign(inv.Member.ID).AssertStatus(s.T(), http.StatusBadRequest)
}

// tenant_admin is tenant-wide by construction: capability checks on tenant-level
// routes (/members, /service-accounts) are scope-agnostic, so a "scoped admin"
// would administer the whole tenant — the API refuses to mint one anywhere.
func (s *MembersSuite) TestTenantAdminMustBeTenantWide() {
	tenant := s.InsertTenant("mb-i", "Members Tenant I").String()
	var entity struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", map[string]any{"name": "Acme Sub", "country": "Germany"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	bad := s.As(tenant).POST(s.T(), "/members", map[string]any{
		"email": "limited@acme.com", "name": "Limited", "role": "tenant_admin", "scopeEntityId": entity.ID,
	})
	bad.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(bad.BodyString(), "tenant-wide")
	var list []member
	s.As(tenant).GET(s.T(), "/members?status=invited").DecodeData(s.T(), &list)
	s.Require().Empty(list)

	memberID, _ := s.inviteAndActivate(tenant, "grace@acme.com", "Grace", "preparer")
	s.As(tenant).PUT(s.T(), "/members/"+memberID+"/role", map[string]any{"role": "tenant_admin", "scopeEntityId": entity.ID}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).POST(s.T(), "/members/"+memberID+"/grants", map[string]any{"role": "tenant_admin", "scopeEntityId": entity.ID}).
		AssertStatus(s.T(), http.StatusBadRequest)
	got := s.getMember(tenant, memberID)
	s.Require().Len(got.Grants, 1)
	s.Require().Equal("preparer", got.Grants[0].Role)
}

// Disabling a member that never accepted its invite, then re-enabling it,
// returns it to invited (it has no password) — a fresh invite onboards it.
func (s *MembersSuite) TestDisabledInviteeReturnsToInvited() {
	tenant := s.InsertTenant("mb-j", "Members Tenant J").String()
	inv := s.invite(tenant, "heidi@acme.com", "Heidi", "viewer")
	s.As(tenant).PUT(s.T(), "/members/"+inv.Member.ID, map[string]any{"name": "Heidi", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusOK)
	// The original token died with the disable.
	s.accept(inv.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusBadRequest)

	var re member
	r := s.As(tenant).PUT(s.T(), "/members/"+inv.Member.ID, map[string]any{"name": "Heidi", "status": "active"})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &re)
	s.Require().Equal("invited", re.Status)

	var fresh invite
	r = s.As(tenant).POST(s.T(), "/members/"+inv.Member.ID+"/invite", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &fresh)
	s.accept(fresh.InviteToken, "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)
	s.login("heidi@acme.com", "correct-horse-battery-staple").AssertStatus(s.T(), http.StatusOK)
	s.Require().Equal("active", s.getMember(tenant, inv.Member.ID).Status)
}
