package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// Logout revokes the current session. A missing session is a no-op (already gone).
func (uc *UseCases) Logout(ctx context.Context, sessionToken string) error {
	session, err := uc.sessions.GetByTokenHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		return err
	}
	if session == nil {
		return nil
	}
	return uc.sessions.Revoke(ctx, session.ID)
}

// LogoutAll revokes every session for a user ("log out everywhere"). The count
// is dropped on purpose: the principal is ending its OWN sessions, which is
// operational hygiene, not the access-review event a disable is — that path
// records it (member.credentials_revoked).
func (uc *UseCases) LogoutAll(ctx context.Context, userID string) error {
	_, err := uc.sessions.RevokeAllForUser(ctx, userID)
	return err
}
