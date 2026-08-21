package dto

import "github.com/mohamadhallal/zentax-api/modules/identity/domain"

// UserToJSON renders the public view of a user — never the password hash or TOTP
// secret.
func UserToJSON(u *domain.User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"tenantId":   u.TenantID,
		"email":      u.Email,
		"name":       u.Name,
		"status":     u.Status,
		"mfaEnabled": u.TOTPEnabled,
		"createdAt":  u.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func LoginResultToJSON(res *domain.LoginResult) map[string]any {
	return map[string]any{
		"mfaRequired": res.MFARequired,
		"user":        UserToJSON(res.User),
	}
}

func MfaEnrollToJSON(res *domain.MfaEnrollResult) map[string]any {
	return map[string]any{
		"secret":     res.Secret,
		"otpauthUrl": res.OtpauthURL,
	}
}

// ServiceAccountToJSON renders a machine principal (no auth material exists on
// it — service accounts have no password and no MFA).
func ServiceAccountToJSON(u *domain.User) map[string]any {
	return map[string]any{
		"id":        u.ID,
		"name":      u.Name,
		"kind":      u.Kind,
		"status":    u.Status,
		"createdAt": u.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// IssuedTokenToJSON renders a freshly issued token. The cleartext token appears
// HERE AND ONLY HERE — it is never retrievable again (only its hash is stored).
func IssuedTokenToJSON(res *domain.IssueTokenResult) map[string]any {
	return map[string]any{
		"id":        res.Token.ID,
		"token":     res.RawToken,
		"label":     res.Token.Label,
		"expiresAt": res.Token.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
