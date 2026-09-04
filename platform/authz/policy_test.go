package authz

import "testing"

func TestRoleMatrix_SeparationOfDuties(t *testing.T) {
	cases := []struct {
		role Role
		cap  Capability
		want bool
	}{
		// viewer: reads only.
		{RoleViewer, EntityRead, true},
		{RoleViewer, EntityWrite, false},
		{RoleViewer, TaskWrite, false},
		{RoleViewer, TaskApprove, false},

		// preparer: prepares + submits, never approves, never sets up.
		{RolePreparer, TaskRead, true},
		{RolePreparer, TaskWrite, true},
		{RolePreparer, TaskSubmit, true},
		{RolePreparer, TaskApprove, false},
		{RolePreparer, WorkflowWrite, false},

		// reviewer: approves, does not prepare or set up.
		{RoleReviewer, TaskApprove, true},
		{RoleReviewer, TaskWrite, false},
		{RoleReviewer, TaskSubmit, false},
		{RoleReviewer, WorkflowWrite, false},

		// manager: full program + approval, but not member management.
		{RoleManager, WorkflowWrite, true},
		{RoleManager, EntityWrite, true},
		{RoleManager, TaskApprove, true},
		{RoleManager, MemberManage, false},

		// tenant_admin: everything.
		{RoleTenantAdmin, MemberManage, true},
		{RoleTenantAdmin, WorkflowWrite, true},
		{RoleTenantAdmin, TaskApprove, true},

		// member:read (the tenant directory) is held by every role; member:manage
		// (invite / disable / roles) by tenant_admin only.
		{RoleViewer, MemberRead, true},
		{RolePreparer, MemberRead, true},
		{RoleReviewer, MemberRead, true},
		{RoleManager, MemberRead, true},
		{RoleTenantAdmin, MemberRead, true},
		{RoleViewer, MemberManage, false},
		{RolePreparer, MemberManage, false},
		{RoleReviewer, MemberManage, false},

		// audit:read is an oversight capability: reviewer/manager/admin only.
		{RoleViewer, AuditRead, false},
		{RolePreparer, AuditRead, false},
		{RoleReviewer, AuditRead, true},
		{RoleManager, AuditRead, true},
		{RoleTenantAdmin, AuditRead, true},
	}
	for _, c := range cases {
		if got := RoleHasCapability(c.role, c.cap); got != c.want {
			t.Errorf("RoleHasCapability(%s, %s) = %v, want %v", c.role, c.cap, got, c.want)
		}
	}
}

func TestHasCapability_UnionOverGrants(t *testing.T) {
	// A user granted both viewer and reviewer can approve (from reviewer) and
	// read (from either) but still cannot write setup.
	grants := []Grant{{Role: RoleViewer}, {Role: RoleReviewer}}
	if !HasCapability(grants, TaskApprove) {
		t.Error("union of viewer+reviewer should include task:approve")
	}
	if !HasCapability(grants, EntityRead) {
		t.Error("union should include entity:read")
	}
	if HasCapability(grants, EntityWrite) {
		t.Error("union of viewer+reviewer must not include entity:write")
	}
}

func TestHasCapability_EmptyGrantsDenies(t *testing.T) {
	if HasCapability(nil, EntityRead) {
		t.Error("a user with no grants must have no capabilities")
	}
}

func TestHumanOnly_ApprovalAndMemberAdmin(t *testing.T) {
	for _, cap := range []Capability{TaskApprove, MemberManage} {
		if !HumanOnly(cap) {
			t.Errorf("%s must be human-only", cap)
		}
	}
	for _, cap := range []Capability{TaskWrite, TaskSubmit, MemberRead, EntityWrite} {
		if HumanOnly(cap) {
			t.Errorf("%s must stay open to service principals", cap)
		}
	}
}
