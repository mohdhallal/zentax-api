package domain

import "time"

// TokenPrefix marks ZenTax API tokens ("ztx_..."), making leaked credentials
// recognizable to secret scanners and letting the auth middleware route bearer
// values cheaply.
const TokenPrefix = "ztx_"

// APIToken is the bearer credential of a service account (machine identity).
// The raw token is shown once at issuance and stored only as a SHA-256 hash.
// Not tenant-RLS-scoped (looked up by hash before tenant context exists), so
// every tenant-sensitive query scopes by TenantID explicitly in the app layer.
type APIToken struct {
	ID         string     `db:"id"`
	TokenHash  string     `db:"token_hash"`
	UserID     string     `db:"user_id"`
	TenantID   string     `db:"tenant_id"`
	Label      string     `db:"label"`
	CreatedBy  *string    `db:"created_by"`
	CreatedAt  time.Time  `db:"created_at"`
	ExpiresAt  time.Time  `db:"expires_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
	RevokedAt  *time.Time `db:"revoked_at"`
}

// IsValid reports whether the token is usable: not revoked and not expired.
func (t *APIToken) IsValid(now time.Time) bool {
	return t.RevokedAt == nil && t.ExpiresAt.After(now)
}

type CreateAPITokenInput struct {
	TokenHash string
	UserID    string
	TenantID  string
	Label     string
	CreatedBy *string
	ExpiresAt time.Time
}

// --- service-account use-case contract types ---

type CreateServiceAccountInput struct {
	Name          string
	Role          string  // granted via user_grants, same matrix as humans
	ScopeEntityID *string // nil = tenant-wide grant
}

type IssueTokenResult struct {
	Token    *APIToken
	RawToken string // cleartext, returned exactly once
}
