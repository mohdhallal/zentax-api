package domain

import "time"

// Session is a server-side session (ADR-0011). The cookie carries an opaque
// token; only its hash is stored.
type Session struct {
	ID         string `db:"id"`
	TokenHash  string `db:"token_hash"`
	UserID     string `db:"user_id"`
	TenantID   string `db:"tenant_id"`
	MFAPending bool   `db:"mfa_pending"`
	// MFAAttempts is the second-factor attempt budget already spent on this
	// session: wrong codes submitted to MFA verify (while pending) or to
	// enrolment confirmation (once full), saturating at the threshold.
	// Spending it destroys what was being guessed — the policy is stated in
	// modules/identity/usecases/mfa.go.
	MFAAttempts       int        `db:"mfa_attempts"`
	CreatedAt         time.Time  `db:"created_at"`
	IdleExpiresAt     time.Time  `db:"idle_expires_at"`
	AbsoluteExpiresAt time.Time  `db:"absolute_expires_at"`
	RevokedAt         *time.Time `db:"revoked_at"`
	IP                *string    `db:"ip"`
	UserAgent         *string    `db:"user_agent"`
}

// IsValid reports whether the session is live at now (not revoked, within idle
// and absolute windows).
func (s *Session) IsValid(now time.Time) bool {
	return s.RevokedAt == nil && s.IdleExpiresAt.After(now) && s.AbsoluteExpiresAt.After(now)
}

type CreateSessionInput struct {
	TokenHash         string
	UserID            string
	TenantID          string
	MFAPending        bool
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	IP                *string
	UserAgent         *string
}
