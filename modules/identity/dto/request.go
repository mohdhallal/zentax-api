package dto

type LoginBody struct {
	Email    string `json:"email"    validate:"required,email" example:"admin@acme.com"`
	Password string `json:"password" validate:"required,min=1,max=200"`
}

// MfaCodeBody is the 6-digit TOTP code for enable / verify.
type MfaCodeBody struct {
	Code string `json:"code" validate:"required,len=6,numeric" example:"123456"`
}

// --- tenant (account settings, ADR-0003) ---

// UpdateTenantBody renames the caller's tenant and sets its IANA timezone;
// the zone name is validated against Go's tz database in the use case.
type UpdateTenantBody struct {
	Name     string `json:"name"     validate:"required,min=1,max=200" example:"Acme GmbH"`
	Timezone string `json:"timezone" validate:"required,min=1,max=64"  example:"Europe/London"`
}

// --- service accounts / API tokens (machine identity) ---

type CreateServiceAccountBody struct {
	Name          string  `json:"name"          validate:"required,min=1,max=200" example:"filing-agent"`
	Role          string  `json:"role"          validate:"required,oneof=tenant_admin manager reviewer preparer viewer" example:"preparer"`
	ScopeEntityID *string `json:"scopeEntityId" validate:"omitempty,uuid"`
}

type IssueTokenBody struct {
	Label         string `json:"label"         validate:"required,min=1,max=100" example:"mcp-server"`
	ExpiresInDays int    `json:"expiresInDays" validate:"omitempty,min=1,max=365" example:"90"`
}

type ServiceAccountIdParams struct {
	ID string `json:"id" validate:"required,uuid"`
}

type TokenIdParams struct {
	ID string `json:"id" validate:"required,uuid"`
}

// --- members (tenant directory, invites, roles) ---

// ListMembersQuery pages and filters the tenant directory. search is a literal,
// case-insensitive substring of the member's name or email (% _ \ match
// themselves); blank means no search, and the total counts the same predicate
// as the page.
type ListMembersQuery struct {
	Limit  int     `json:"limit"  default:"100" validate:"min=1,max=500" example:"100"`
	Offset int     `json:"offset" default:"0"   validate:"min=0" example:"0"`
	Kind   *string `json:"kind"   validate:"omitempty,oneof=human service" example:"human"`
	Status *string `json:"status" validate:"omitempty,oneof=active invited disabled" example:"active"`
	Search string  `json:"search" validate:"omitempty,max=200" example:"jane"`
}

type CreateMemberBody struct {
	Email         string  `json:"email"         validate:"required,email,max=254" example:"jane@acme.com"`
	Name          string  `json:"name"          validate:"required,min=1,max=200" example:"Jane Doe"`
	Role          string  `json:"role"          validate:"required,oneof=tenant_admin manager reviewer preparer viewer" example:"preparer"`
	ScopeEntityID *string `json:"scopeEntityId" validate:"omitempty,uuid"`
}

type UpdateMemberBody struct {
	Name   string `json:"name"   validate:"required,min=1,max=200" example:"Jane Doe"`
	Status string `json:"status" validate:"required,oneof=active disabled" example:"active"`
}

type GrantBody struct {
	Role          string  `json:"role"          validate:"required,oneof=tenant_admin manager reviewer preparer viewer" example:"reviewer"`
	ScopeEntityID *string `json:"scopeEntityId" validate:"omitempty,uuid"`
}

type MemberIdParams struct {
	ID string `json:"id" validate:"required,uuid"`
}

type MemberGrantParams struct {
	ID      string `json:"id"      validate:"required,uuid"`
	GrantID string `json:"grantId" validate:"required,uuid"`
}

// AcceptInviteBody redeems an invite token (public route). The password
// policy is length-based (NIST 800-63B): min 12, max 200.
type AcceptInviteBody struct {
	Token    string  `json:"token"    validate:"required,min=8,max=200" example:"zti_..."`
	Password string  `json:"password" validate:"required,min=12,max=200"`
	Name     *string `json:"name"     validate:"omitempty,min=1,max=200" example:"Jane Doe"`
}
