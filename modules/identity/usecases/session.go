package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// Authenticate resolves a cookie token to its (session, user), sliding the idle
// window. Used by the RequireSession middleware. It does NOT reject mfa_pending
// sessions — that policy belongs to the middleware, which knows the route.
func (uc *UseCases) Authenticate(ctx context.Context, token string) (*domain.Session, *domain.User, error) {
	if token == "" {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	now := uc.now()

	session, err := uc.sessions.GetByTokenHash(ctx, crypto.HashToken(token))
	if err != nil {
		return nil, nil, err
	}
	if session == nil || !session.IsValid(now) {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}

	user, err := uc.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}

	newIdle := now.Add(uc.settings.SessionIdleTTL)
	_ = uc.sessions.Touch(ctx, session.ID, newIdle)
	session.IdleExpiresAt = newIdle

	return session, user, nil
}

// Me returns the current user record.
func (uc *UseCases) Me(ctx context.Context, userID string) (*domain.User, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	return user, nil
}
