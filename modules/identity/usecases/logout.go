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

// LogoutAll revokes every session for a user ("log out everywhere").
func (uc *UseCases) LogoutAll(ctx context.Context, userID string) error {
	return uc.sessions.RevokeAllForUser(ctx, userID)
}
