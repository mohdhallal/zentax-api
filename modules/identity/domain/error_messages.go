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
)
