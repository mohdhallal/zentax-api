package domain

import "time"

// Session is a server-side session (ADR-0011). The cookie carries an opaque
// token; only its hash is stored.
type Session struct {
	ID                string     `db:"id"`
	TokenHash         string     `db:"token_hash"`
	UserID            string     `db:"user_id"`
	TenantID          string     `db:"tenant_id"`
	MFAPending        bool       `db:"mfa_pending"`
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
