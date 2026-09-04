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

// MemberToJSON renders the directory view of a user: no auth material, grants
// embedded with the scope entity's name resolved at read time.
func MemberToJSON(m *domain.Member) map[string]any {
	grants := make([]map[string]any, 0, len(m.Grants))
	for i := range m.Grants {
		g := &m.Grants[i]
		grants = append(grants, map[string]any{
			"id":              g.ID,
			"role":            g.Role,
			"scopeEntityId":   g.ScopeEntityID,
			"scopeEntityName": g.ScopeEntityName,
		})
	}
	return map[string]any{
		"id":         m.ID,
		"name":       m.Name,
		"email":      m.Email,
		"kind":       m.Kind,
		"status":     m.Status,
		"mfaEnabled": m.MFAEnabled,
		"grants":     grants,
		"createdAt":  m.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// InviteToJSON renders a freshly issued invite. The cleartext invite token
// appears HERE AND ONLY HERE (only its hash is stored). withMember embeds the
// member (first invite) or not (re-issue).
func InviteToJSON(res *domain.InviteResult, withMember bool) map[string]any {
	out := map[string]any{
		"inviteToken":     res.RawToken,
		"inviteExpiresAt": res.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if withMember && res.Member != nil {
		out["member"] = MemberToJSON(res.Member)
	}
	return out
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
