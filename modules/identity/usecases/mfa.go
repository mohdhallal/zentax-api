package usecases

import (
	"context"

	"github.com/pquerna/otp/totp"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// MfaEnroll generates a TOTP secret, stores it encrypted (pending, not yet
// enabled), and returns the secret + otpauth URL for the authenticator app.
func (uc *UseCases) MfaEnroll(ctx context.Context, userID string) (*domain.MfaEnrollResult, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	if user.TOTPEnabled {
		return nil, apperrors.NewConflict(domain.MsgMFAAlreadyEnabled)
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: uc.settings.TOTPIssuer, AccountName: user.Email})
	if err != nil {
		return nil, err
	}
	enc, err := crypto.Encrypt(uc.settings.EncryptionKey, []byte(key.Secret()))
	if err != nil {
		return nil, err
	}
	if err := uc.users.SetTOTP(ctx, userID, &enc, false); err != nil {
		return nil, err
	}

	return &domain.MfaEnrollResult{Secret: key.Secret(), OtpauthURL: key.URL()}, nil
}

// MfaEnable confirms an enrolled secret with a valid code and turns MFA on.
func (uc *UseCases) MfaEnable(ctx context.Context, userID, code string) error {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	if user.TOTPSecretEnc == nil {
		return apperrors.NewValidation(domain.MsgMFANotEnrolled)
	}
	secret, err := crypto.Decrypt(uc.settings.EncryptionKey, *user.TOTPSecretEnc)
	if err != nil {
		return err
	}
	if !totp.Validate(code, string(secret)) {
		return apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}
	return uc.users.SetTOTP(ctx, userID, user.TOTPSecretEnc, true)
}

// MfaVerify completes an mfa_pending session: it validates the code, clears
// mfa_pending, and rotates the session token (fixation defense). Returns the new
// cookie token.
func (uc *UseCases) MfaVerify(ctx context.Context, sessionToken, code string) (*domain.LoginResult, error) {
	now := uc.now()

	session, err := uc.sessions.GetByTokenHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		return nil, err
	}
	if session == nil || !session.IsValid(now) || !session.MFAPending {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}

	user, err := uc.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TOTPSecretEnc == nil {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	secret, err := crypto.Decrypt(uc.settings.EncryptionKey, *user.TOTPSecretEnc)
	if err != nil {
		return nil, err
	}
	if !totp.Validate(code, string(secret)) {
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	newToken, err := crypto.NewSessionToken()
	if err != nil {
		return nil, err
	}
	if err := uc.sessions.CompleteMFA(ctx, session.ID, crypto.HashToken(newToken),
		now.Add(uc.settings.SessionIdleTTL), now.Add(uc.settings.SessionAbsoluteTTL)); err != nil {
		return nil, err
	}

	return &domain.LoginResult{SessionToken: newToken, MFARequired: false, User: user}, nil
}
