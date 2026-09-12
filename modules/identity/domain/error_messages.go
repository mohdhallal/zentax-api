package domain

// Auth error messages. Credential errors are deliberately generic to avoid user
// enumeration.
//
// There is deliberately NO "account locked" message. One existed and was
// retired: /auth/login answered it as soon as the submitted password matched a
// locked account's stored hash, which made the lockout a password oracle — an
// attacker who had just spent five requests locking an address could then read
// the correct password off the responses while the lock refused nothing. A
// locked account now answers MsgInvalidCredentials like every other failure
// (the policy is stated in modules/identity/usecases/login.go). Do not
// reintroduce a message that distinguishes it; tell the owner out of band
// instead (lockout notification e-mail / self-service unlock).
const (
	MsgInvalidCredentials = "invalid email or password"
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

	// Federated identity and broker-owned credential recovery (ADR-0011). The
	// broker is Phase 2 with no implementation in any edition, so this is what
	// the seam answers today — see broker.go. It describes the DEPLOYMENT's
	// configuration and nothing about the subject, so it is safe to return for
	// any address: an unknown one and a real one get the same sentence.
	MsgNoIdentityProvider = "no identity provider is configured"
)
