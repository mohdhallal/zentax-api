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
