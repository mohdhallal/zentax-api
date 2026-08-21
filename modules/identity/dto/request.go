package dto

type LoginBody struct {
	Email    string `json:"email"    validate:"required,email" example:"admin@acme.com"`
	Password string `json:"password" validate:"required,min=1,max=200"`
}

// MfaCodeBody is the 6-digit TOTP code for enable / verify.
type MfaCodeBody struct {
	Code string `json:"code" validate:"required,len=6,numeric" example:"123456"`
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
