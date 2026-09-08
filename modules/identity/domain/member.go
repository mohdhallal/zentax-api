package domain

import "time"

// Member administration (tenant directory, invites, roles). A member IS a
// users row — human or service — seen through the tenant-admin surface, with
// its RBAC grants embedded. users is NOT RLS-scoped, so every members query
// filters tenant_id explicitly from the requester's tenant; user_grants and
// entities ARE RLS-scoped and ride the request transaction.

// User statuses (users.status).
const (
	StatusActive   = "active"
	StatusInvited  = "invited"
	StatusDisabled = "disabled"
)

// InviteTokenPrefix marks invite tokens ("zti_..."), distinct from API tokens
// ("ztx_") so a leaked value is recognizable and never routes as a bearer.
const InviteTokenPrefix = "zti_"

// InviteTTL is the fixed lifetime of an invite token.
const InviteTTL = 7 * 24 * time.Hour

// MemberGrant is one user_grants row as the directory renders it: the role,
// its scope (nil = tenant-wide) and the scope entity's name resolved at read
// time through an RLS-scoped join.
type MemberGrant struct {
	ID              string  `db:"id"`
	UserID          string  `db:"user_id"`
	Role            string  `db:"role"`
	ScopeEntityID   *string `db:"scope_entity_id"`
	ScopeEntityName *string `db:"scope_entity_name"`
}

// IsTenantWide reports whether the grant covers the whole tenant.
func (g *MemberGrant) IsTenantWide() bool { return g.ScopeEntityID == nil }

// Member is the directory view of a user: no auth material, grants embedded.
type Member struct {
	ID         string `db:"id"`
	TenantID   string `db:"tenant_id"`
	Email      string `db:"email"`
	Name       string `db:"name"`
	Kind       string `db:"kind"`
	Status     string `db:"status"`
	MFAEnabled bool   `db:"totp_enabled"`
	// HasPassword: a human that never accepted an invite has none (never
	// exposed — it only decides whether re-enabling yields active or invited).
	HasPassword bool      `db:"has_password"`
	CreatedAt   time.Time `db:"created_at"`
	Grants      []MemberGrant
}

func (m *Member) IsActive() bool  { return m.Status == StatusActive }
func (m *Member) IsInvited() bool { return m.Status == StatusInvited }
func (m *Member) IsService() bool { return m.Kind == KindService }

// InviteToken is the one-time credential an invited human redeems to set a
// password and activate. Stored ONLY as a SHA-256 hash (cleartext shown once,
// like API tokens); expiry is mandatory; revocation is a tombstone. Not
// RLS-scoped — looked up by hash before any tenant context exists — so the
// tenant is carried on the row and bound explicitly when the invite is accepted.
type InviteToken struct {
	ID         string     `db:"id"`
	TokenHash  string     `db:"token_hash"`
	UserID     string     `db:"user_id"`
	TenantID   string     `db:"tenant_id"`
	CreatedBy  *string    `db:"created_by"`
	CreatedAt  time.Time  `db:"created_at"`
	ExpiresAt  time.Time  `db:"expires_at"`
	AcceptedAt *time.Time `db:"accepted_at"`
	RevokedAt  *time.Time `db:"revoked_at"`
}

// IsUsable reports whether the token can still be redeemed: not accepted,
// not revoked, not expired.
func (t *InviteToken) IsUsable(now time.Time) bool {
	return t.AcceptedAt == nil && t.RevokedAt == nil && t.ExpiresAt.After(now)
}

type CreateInviteTokenInput struct {
	TokenHash string
	UserID    string
	TenantID  string
	CreatedBy *string
	ExpiresAt time.Time
}

// --- use-case contract types ---

type ListMembersArgs struct {
	TenantID string
	Kind     string // "" = all
	Status   string // "" = all
	// Search is a trimmed free-text term matched as a literal, case-insensitive
	// substring of name or email; "" = no search. Page and total share it.
	Search string
	Limit  int
	Offset int
}

type CreateMemberInput struct {
	Email         string
	Name          string
	Role          string
	ScopeEntityID *string // nil = tenant-wide grant
}

type UpdateMemberInput struct {
	Name   string
	Status string // active | disabled
}

type GrantInput struct {
	Role          string
	ScopeEntityID *string
}

// InviteResult carries the cleartext invite token — returned exactly once.
type InviteResult struct {
	Member    *Member
	RawToken  string
	ExpiresAt time.Time
}

type AcceptInviteInput struct {
	Token    string
	Password string
	Name     *string // optional display-name override
}

type AcceptInviteResult struct {
	Email string
}
