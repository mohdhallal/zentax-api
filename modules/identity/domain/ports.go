package domain

import (
	"context"
	"time"
)

type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	// ListByKind returns a tenant's users of one kind (users is not RLS'd, so
	// the tenant is scoped explicitly).
	ListByKind(ctx context.Context, tenantID, kind string) ([]User, error)
	// RecordFailedLogin sets the failed-attempt counter and optional lock time.
	RecordFailedLogin(ctx context.Context, id string, attempts int, lockedUntil *time.Time) error
	ResetFailedLogin(ctx context.Context, id string) error
	SetTOTP(ctx context.Context, id string, secretEnc *string, enabled bool) error
}

type SessionRepository interface {
	Create(ctx context.Context, input CreateSessionInput) (*Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Touch(ctx context.Context, id string, idleExpiresAt time.Time) error
	// CompleteMFA clears mfa_pending and rotates the token (fixation defense).
	CompleteMFA(ctx context.Context, id, newTokenHash string, idleExpiresAt, absoluteExpiresAt time.Time) error
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
}

// SessionAuthenticator resolves a session-cookie token to its session + user.
// Consumed by the RequireAuth middleware (kept narrow so the middleware does
// not depend on the whole use-case surface).
type SessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*Session, *User, error)
}

// TokenAuthenticator resolves a bearer API token ("ztx_...") to its token +
// service-account user (machine identity, agentic-AI readiness B1).
type TokenAuthenticator interface {
	AuthenticateToken(ctx context.Context, raw string) (*APIToken, *User, error)
}

// RequestAuthenticator is what the auth middleware needs: both credential
// forms. Implemented by the identity use cases.
type RequestAuthenticator interface {
	SessionAuthenticator
	TokenAuthenticator
}

// TokenRepository persists API tokens. Not RLS-scoped — tenant-sensitive
// queries take the tenant explicitly and must scope by it.
type TokenRepository interface {
	Create(ctx context.Context, input CreateAPITokenInput) (*APIToken, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*APIToken, error)
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
	// Revoke tombstones a token within a tenant; returns false if no live
	// token matched (wrong tenant, unknown id, or already revoked).
	Revoke(ctx context.Context, id, tenantID string) (bool, error)
}

// GrantWriter writes one RBAC grant (user_grants is RLS-scoped; runs on the
// request transaction). Implemented by the identity GrantRepo.
type GrantWriter interface {
	Insert(ctx context.Context, userID, role string, scopeEntityID *string) error
}

// ServiceAccountUseCases is the tenant-admin surface for machine identities.
// All of it is gated by the member:manage capability at the route layer.
type ServiceAccountUseCases interface {
	CreateServiceAccount(ctx context.Context, input CreateServiceAccountInput) (*User, error)
	ListServiceAccounts(ctx context.Context) ([]User, error)
	IssueToken(ctx context.Context, serviceAccountID, label string, ttlDays int) (*IssueTokenResult, error)
	RevokeToken(ctx context.Context, tokenID string) error
}

// AuthUseCases is the handler-facing auth surface.
type AuthUseCases interface {
	Login(ctx context.Context, input LoginInput) (*LoginResult, error)
	MfaEnroll(ctx context.Context, userID string) (*MfaEnrollResult, error)
	MfaEnable(ctx context.Context, userID, code string) error
	MfaVerify(ctx context.Context, sessionToken, code string) (*LoginResult, error)
	Logout(ctx context.Context, sessionToken string) error
	LogoutAll(ctx context.Context, userID string) error
	Me(ctx context.Context, userID string) (*User, error)
}

// --- use-case contract types ---

type LoginInput struct {
	Email     string
	Password  string
	IP        *string
	UserAgent *string
}

// LoginResult carries the raw session token (for the Set-Cookie) and whether an
// MFA step is still required before the session is usable.
type LoginResult struct {
	SessionToken string
	MFARequired  bool
	User         *User
}

type MfaEnrollResult struct {
	Secret     string // base32 TOTP secret (manual entry)
	OtpauthURL string // otpauth:// URI (QR)
}
