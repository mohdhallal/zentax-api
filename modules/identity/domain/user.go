package domain

import "time"

// User is the app-owned identity record (ADR-0011). Not tenant-RLS-scoped —
// login resolves a user by email before any tenant context exists.
type User struct {
	ID                  string     `db:"id"`
	TenantID            string     `db:"tenant_id"`
	Email               string     `db:"email"`
	Name                string     `db:"name"`
	Kind                string     `db:"kind"` // human | service
	PasswordHash        *string    `db:"password_hash"`
	Status              string     `db:"status"`
	TOTPSecretEnc       *string    `db:"totp_secret_enc"`
	TOTPEnabled         bool       `db:"totp_enabled"`
	FailedLoginAttempts int        `db:"failed_login_attempts"`
	LockedUntil         *time.Time `db:"locked_until"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
}

func (u *User) IsActive() bool { return u.Status == "active" }

// IsService reports whether this is a machine principal (service account): it
// can never log in interactively, and can never approve (ADR-0012 — approval
// is human-only).
func (u *User) IsService() bool { return u.Kind == KindService }

func (u *User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

const (
	KindHuman   = "human"
	KindService = "service"
)

type CreateUserInput struct {
	TenantID     string
	Email        string
	Name         string
	Kind         string // defaults to human when empty
	PasswordHash *string
	Status       string
}
