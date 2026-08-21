package usecases

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// Login verifies credentials and mints a session. If the user has TOTP enabled,
// the session is mfa_pending (unusable for tenant routes until MFA verify).
func (uc *UseCases) Login(ctx context.Context, input domain.LoginInput) (*domain.LoginResult, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	now := uc.now()

	user, err := uc.users.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	// Generic failure + a dummy verify to keep timing independent of existence.
	// Service accounts never log in interactively — API tokens only.
	if user == nil || user.IsService() || user.PasswordHash == nil || !user.IsActive() {
		_, _ = crypto.VerifyPassword(input.Password, dummyHash)
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidCredentials)
	}
	if user.IsLocked(now) {
		return nil, apperrors.NewUnauthorized(domain.MsgAccountLocked)
	}

	ok, err := crypto.VerifyPassword(input.Password, *user.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		attempts := user.FailedLoginAttempts + 1
		var lockedUntil *time.Time
		if attempts >= maxFailedAttempts {
			t := now.Add(lockoutDuration)
			lockedUntil = &t
		}
		_ = uc.users.RecordFailedLogin(ctx, user.ID, attempts, lockedUntil)
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidCredentials)
	}

	if user.FailedLoginAttempts > 0 {
		_ = uc.users.ResetFailedLogin(ctx, user.ID)
	}

	token, _, err := uc.createSession(ctx, user, user.TOTPEnabled, input.IP, input.UserAgent)
	if err != nil {
		return nil, err
	}

	return &domain.LoginResult{
		SessionToken: token,
		MFARequired:  user.TOTPEnabled,
		User:         user,
	}, nil
}
