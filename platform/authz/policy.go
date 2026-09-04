// Package authz defines the capability model and the role→capability matrix for
// scoped RBAC (ADR-0012).
//
// A capability is a fine-grained permission a route requires (e.g. entity:write,
// task:approve). A user is granted one or more roles via user_grants; their
// effective capabilities are the union of the capabilities of those roles.
//
// This package answers the *tenant-wide* question — "does this user hold
// capability X anywhere in the tenant" — which is enough to enforce role-based
// separation of duties (a viewer cannot write; a preparer cannot approve).
// Per-entity *scope* of a grant (scope_entity_id → an entity subtree) is a
// second gate enforced per-resource in the handlers; see AuthorizeScope once it
// lands. Until then, a grant's scope is not yet narrowed — only tenant-wide
// grants are issued (cmd/seed-admin), so the tenant-wide check is exact for the
// grants that exist today.
package authz

// Capability is a fine-grained permission required by a route.
type Capability string

const (
	EntityRead  Capability = "entity:read"
	EntityWrite Capability = "entity:write"

	ObligationTypeRead  Capability = "obligation_type:read"
	ObligationTypeWrite Capability = "obligation_type:write"

	EntityObligationRead  Capability = "entity_obligation:read"
	EntityObligationWrite Capability = "entity_obligation:write"

	WorkflowRead  Capability = "workflow:read"
	WorkflowWrite Capability = "workflow:write"

	WorkflowTaskRead  Capability = "workflow_task:read"
	WorkflowTaskWrite Capability = "workflow_task:write"

	TaskRead    Capability = "task:read"
	TaskWrite   Capability = "task:write"   // edit a task instance's status / tax data
	TaskSubmit  Capability = "task:submit"  // submit a task instance for approval
	TaskApprove Capability = "task:approve" // approve / reject a submitted task instance

	// MemberRead lists the tenant directory (names, emails, roles). Held by
	// every role: any member must be able to see who can be assigned a task.
	MemberRead   Capability = "member:read"
	MemberManage Capability = "member:manage" // manage tenant users + their grants (invite, disable, roles)

	// AuditRead reads the application audit trail (ADR-0008 stream 1). Held by
	// the oversight roles only — reviewer, manager, tenant_admin — never by a
	// viewer or preparer: who-did-what is evidence for the people accountable
	// for the compliance program, not general-purpose read data. Not human-only:
	// a service principal may hold it like any other read.
	AuditRead Capability = "audit:read"
)

// Role is a named bundle of capabilities granted to a user (user_grants.role).
type Role string

const (
	RoleTenantAdmin Role = "tenant_admin"
	RoleManager     Role = "manager"
	RoleReviewer    Role = "reviewer"
	RolePreparer    Role = "preparer"
	RoleViewer      Role = "viewer"
)

// reads: every read capability, held by every role (any tenant member may view).
var reads = []Capability{
	EntityRead, ObligationTypeRead, EntityObligationRead,
	WorkflowRead, WorkflowTaskRead, TaskRead, MemberRead,
}

// setupWrites: the "administer the compliance program" writes — defining
// entities, obligation types, obligations, workflows and their task templates.
var setupWrites = []Capability{
	EntityWrite, ObligationTypeWrite, EntityObligationWrite,
	WorkflowWrite, WorkflowTaskWrite,
}

// roleCapabilities is the role → capability matrix (ADR-0012). It mirrors the
// frontend rolePermissions intent: admins manage everything incl. members;
// managers run the whole compliance program and approve; reviewers approve;
// preparers fill and submit task data; viewers read only.
var roleCapabilities = buildMatrix()

func buildMatrix() map[Role]map[Capability]bool {
	m := map[Role]map[Capability]bool{
		RoleTenantAdmin: {}, RoleManager: {}, RoleReviewer: {},
		RolePreparer: {}, RoleViewer: {},
	}
	add := func(role Role, caps ...Capability) {
		for _, c := range caps {
			m[role][c] = true
		}
	}

	// viewer — read-only across the board.
	add(RoleViewer, reads...)

	// preparer — reads + works task instances (fills tax data, submits for approval).
	// Deliberately NOT task:approve (separation of duties) and NOT setup writes.
	add(RolePreparer, reads...)
	add(RolePreparer, TaskWrite, TaskSubmit)

	// reviewer — reads + approves/rejects submitted task instances (the approver
	// side of SoD). No setup writes, no preparing.
	add(RoleReviewer, reads...)
	add(RoleReviewer, TaskApprove, AuditRead)

	// manager — the full compliance program: all reads, all setup writes, and the
	// whole task lifecycle including approval. No member management.
	add(RoleManager, reads...)
	add(RoleManager, setupWrites...)
	add(RoleManager, TaskWrite, TaskSubmit, TaskApprove, AuditRead)

	// tenant_admin — everything the manager has, plus member management.
	add(RoleTenantAdmin, reads...)
	add(RoleTenantAdmin, setupWrites...)
	add(RoleTenantAdmin, TaskWrite, TaskSubmit, TaskApprove, AuditRead, MemberManage)

	return m
}

// RoleHasCapability reports whether a single role includes cap.
func RoleHasCapability(role Role, cap Capability) bool {
	return roleCapabilities[role][cap]
}

// KnownRole reports whether role exists in the matrix (grantable).
func KnownRole(role Role) bool {
	_, ok := roleCapabilities[role]
	return ok
}

// HumanOnly reports whether cap may never be exercised by a service principal,
// regardless of granted roles. Approval is the attestation at the heart of
// segregation of duties (ADR-0012/0018) — a machine can prepare and submit,
// but only a human approves (or rejects). Member administration is human-only
// too: a machine never mints principals or grants (no self-replication) —
// refused at grant time AND here, so a legacy grant cannot reopen it. Enforced
// by the capability middleware and again by the Authorizer (defense in depth).
func HumanOnly(cap Capability) bool {
	return cap == TaskApprove || cap == MemberManage
}

// Grant is one user_grants row, reduced to what authorization needs: the role
// and its scope (nil scope = tenant-wide).
type Grant struct {
	Role          Role
	ScopeEntityID *string
}

// HasCapability reports whether any of the user's granted roles includes cap.
// This is the tenant-wide capability check (scope-agnostic); per-entity scope is
// enforced separately at the resource. An empty grant set never has anything.
func HasCapability(grants []Grant, cap Capability) bool {
	for _, g := range grants {
		if RoleHasCapability(g.Role, cap) {
			return true
		}
	}
	return false
}
