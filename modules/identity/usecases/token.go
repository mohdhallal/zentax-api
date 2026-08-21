package usecases

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// AuthenticateToken resolves a bearer API token to its (token, service-account
// user). Used by the RequireAuth middleware for machine callers. Deliberately
// generic errors — no token/account enumeration.
func (uc *UseCases) AuthenticateToken(ctx context.Context, raw string) (*domain.APIToken, *domain.User, error) {
	if !strings.HasPrefix(raw, domain.TokenPrefix) {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}
	now := uc.now()

	token, err := uc.tokens.GetByTokenHash(ctx, crypto.HashToken(raw))
	if err != nil {
		return nil, nil, err
	}
	if token == nil || !token.IsValid(now) {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}

	user, err := uc.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, nil, err
	}
	// Tokens authenticate machine principals only.
	if user == nil || !user.IsActive() || !user.IsService() {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}

	_ = uc.tokens.TouchLastUsed(ctx, token.ID, now) // best-effort usage stamp

	return token, user, nil
}

func time24h(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
