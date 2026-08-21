package domain

import (
	"context"
	"time"
)

type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	Create(ctx context.Context, input CreateUserInput) (*User, error)
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
// Consumed by the RequireSession middleware (kept narrow so the middleware does
// not depend on the whole use-case surface).
type SessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*Session, *User, error)
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
