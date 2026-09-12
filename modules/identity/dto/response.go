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

// TenantToJSON renders the account record (GET/PUT /tenant).
func TenantToJSON(t *domain.Tenant) map[string]any {
	return map[string]any{
		"id":        t.ID,
		"slug":      t.Slug,
		"name":      t.Name,
		"timezone":  t.Timezone,
		"createdAt": t.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt": t.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// MeToJSON is the /auth/me view: every user key as before, plus the
// caller's tenant (id, slug, name, timezone) so clients render instants and
// "today" in the tenant's zone (ADR-0003 / ADR-0023 §6).
func MeToJSON(u *domain.User, t *domain.Tenant) map[string]any {
	out := UserToJSON(u)
	out["tenant"] = map[string]any{
		"id":       t.ID,
		"slug":     t.Slug,
		"name":     t.Name,
		"timezone": t.Timezone,
	}
	return out
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
// embedded with the scope entity's name resolved at read time — and the scope
// WITHHELD when it points outside the caller's read scope.
//
// The repository resolves scope_entity_name only for a scope entity the caller
// may read, so a grant that HAS a scope entity but no resolved name is one whose
// scope is withheld (see the contract documented on attachGrants in
// modules/identity/repositories/pg/member_repo.go). The id is dropped with the
// name rather than kept: a uuid this caller can resolve nowhere is still a join
// key — it confirms "this member has access to entity X" for any X they can name
// from elsewhere, and counting the distinct withheld ids measures the size of
// the group that is being hidden, which is the same leak a non-narrowing total
// is. `scopeWithheld` then says so explicitly, so a client renders "scoped to an
// entity you cannot see" instead of mistaking a scoped grant for a tenant-wide
// one. For an unbounded caller — every holder of member:manage among them — the
// flag is false on every grant and the view is unchanged.
func MemberToJSON(m *domain.Member) map[string]any {
	grants := make([]map[string]any, 0, len(m.Grants))
	for i := range m.Grants {
		g := &m.Grants[i]
		withheld := g.ScopeEntityID != nil && g.ScopeEntityName == nil
		scopeEntityID := g.ScopeEntityID
		if withheld {
			scopeEntityID = nil
		}
		grants = append(grants, map[string]any{
			"id":              g.ID,
			"role":            g.Role,
			"scopeEntityId":   scopeEntityID,
			"scopeEntityName": g.ScopeEntityName,
			"scopeWithheld":   withheld,
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
