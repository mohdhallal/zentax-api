package domain

// Auth error messages. Credential errors are deliberately generic to avoid user
// enumeration.
const (
	MsgInvalidCredentials = "invalid email or password"
	MsgAccountLocked      = "account temporarily locked after too many failed attempts"
	MsgSessionInvalid     = "session is invalid or expired"
	MsgMFARequired        = "multi-factor authentication required"
	MsgInvalidMFACode     = "invalid authentication code"
	MsgMFANotEnrolled     = "MFA is not enrolled"
	MsgMFAAlreadyEnabled  = "MFA is already enabled"

	// Machine identity (service accounts + API tokens).
	MsgTokenInvalid           = "API token is invalid, expired, or revoked"
	MsgTokenNotFound          = "API token not found"
	MsgServiceAccountNotFound = "service account not found"

	// Member administration. The invite failure is deliberately generic — it
	// never reveals whether a token / email exists or why it was refused.
	MsgMemberNotFound         = "member not found"
	MsgMemberEmailTaken       = "a member with this email already exists"
	MsgMemberNotInvited       = "member is not pending an invitation"
	MsgCannotDisableSelf      = "you cannot disable your own account"
	MsgInvitedNotActivatable  = "an invited member becomes active by accepting the invite"
	MsgLastTenantAdmin        = "the tenant must keep at least one tenant admin"
	MsgServiceCannotBeAdmin   = "a service account cannot be granted tenant_admin"
	MsgAdminMustBeTenantWide  = "tenant_admin is tenant-wide: it cannot be scoped to an entity"
	MsgScopeEntityNotInTenant = "scopeEntityId is not an entity of this tenant"
	MsgGrantNotFound          = "grant not found"
	MsgInviteInvalid          = "invite is invalid or has expired"

	// Tenant settings (ADR-0003). The zone name is echoed so the admin sees
	// what was refused — it is caller input, not PII.
	MsgUnknownTimezone = "unknown IANA timezone: "
	MsgTenantNotFound  = "tenant not found"
)
