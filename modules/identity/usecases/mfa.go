package usecases

import (
	"context"

	"github.com/pquerna/otp/totp"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// THE SECOND-FACTOR ATTEMPT BUDGET. This is the one place it is stated; the
// repository implements it in one statement (SessionRepo.ConsumeMFAAttempt) and
// the sessions.mfa_attempts migration records why the column exists.
//
// WHAT IT BOUNDS: five validations of a TOTP code per SESSION, charged before
// the code is looked at, in one atomic statement — so a thousand simultaneous
// guesses buy the same five validations a thousand serial ones do. When the
// budget is spent, WHAT WAS BEING GUESSED IS DESTROYED: the pending session for
// MFA verify, the pending enrolment for enrolment confirmation. A code that
// verifies clears the counter.
//
// WHY IT HAD TO EXIST. Before this, /auth/mfa/verify counted nothing and
// invalidated nothing on a wrong code: the mfa_pending session survived every
// refusal, so the six digits in front of it could be retried without limit.
// That is an authentication bypass and not a capacity problem — a factor worth
// ~20 guesses an hour to a human is worth the whole million-value space to a
// script, and by the time a pending session exists the password has already been
// given up.
//
//   - FIVE WRONG CODES PER SESSION, then the session is gone and the user signs
//     in again. Per SESSION and not per ACCOUNT on purpose: locking the account
//     on MFA failures would hand anyone who had stolen a password a way to lock
//     the owner out of it, and the thing being guessed belongs to one login
//     attempt, not to the account.
//   - THE ATTEMPT THAT REACHES THE THRESHOLD IS STILL SPENT ON A VALIDATION —
//     it is the last try of the budget, not the first refusal. A correct code on
//     the fifth try still completes the login, which is what makes the budget
//     cheap for the owner and expensive for the attacker.
//   - EVERY ATTEMPT COSTS BUDGET, the successful one included — that is what
//     makes the bound hold under concurrency, since nothing can be known about
//     the code at the moment the charge is made. Success then clears the counter
//     in the same statement that completes the session, so serial logins never
//     accumulate debt.
//   - CHARGED OUTSIDE ANY REQUEST TRANSACTION. The tx middleware rolls a request
//     transaction back on any status >= 400, which would discard the charge
//     every refusal depends on — the exact failure that once made the login
//     lockout unreachable. Both MFA code routes therefore declare no transaction
//     and this use case owns its writes (see MfaVerifyHandler.DefineRoute).
//   - ONE REFUSAL, for everything. An expired pending session, a spent budget, a
//     destroyed session and a wrong code all answer MsgInvalidMFACode. A
//     distinguishable "your session is gone" tells a script exactly where the
//     budget ends and when to start a fresh login; a distinguishable "budget
//     spent" is the same signal one attempt earlier. The price is the one
//     /auth/login already pays: a user whose pending session merely expired is
//     told only that the code is invalid, and has to sign in again.
//   - WHAT THIS DOES NOT BUY is rate limiting. Nothing here bounds an attacker
//     spreading five guesses each across many freshly minted pending sessions —
//     though each of those logins costs a password verification and a charge
//     against the login budget of its own. Per-IP / per-principal limits remain
//     listed under "Not covered" in STATUS.md's auth section.
const maxMFAAttempts = 5

// MfaEnroll generates a TOTP secret, stores it encrypted (pending, not yet
// enabled), and returns the secret + otpauth URL for the authenticator app.
//
// No budget applies here: nothing is guessed. The caller is handed the secret it
// would otherwise be brute-forcing — which is also why enrolment confirmation's
// budget below is a hygiene bound and not an authentication control.
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
//
// It resolves the session itself rather than taking the requester the auth
// middleware would have bound, because its route can no longer declare a tenant:
// a tenant implies a request transaction, and a charge written inside one is
// rolled back with the 401 it answers. The checks it performs are exactly the
// ones RequireAuth performed — a live session, an active user, and MFA not still
// pending — with one behavioural change: a bearer API token no longer reaches
// this route. That is intended; a service account has no authenticator app.
//
// The budget here is hygiene rather than an authentication control, and the flow
// says why: the secret being confirmed was handed to this same caller in
// cleartext by MfaEnroll, so guessing it buys nothing an attacker could not
// simply read. What the budget stops is an unbounded decrypt-and-validate loop
// on an authenticated route. Spending it destroys the PENDING ENROLMENT rather
// than the session — the proportionate "start again" for a confirmation step —
// and the next enrolment gets a fresh secret with a fresh budget.
func (uc *UseCases) MfaEnable(ctx context.Context, sessionToken, code string) error {
	session, user, err := uc.fullSession(ctx, sessionToken)
	if err != nil {
		return err
	}

	// The enrolment state is read BEFORE the charge — unlike MfaVerify, which
	// charges first. Nothing read here depends on the code, so the budget still
	// bounds validations exactly; what it avoids is a destroyed enrolment
	// leaving the session's counter creeping toward saturation on "not enrolled"
	// refusals, which would lock the owner out of confirming a fresh one.
	if user.TOTPSecretEnc == nil {
		return apperrors.NewValidation(domain.MsgMFANotEnrolled)
	}
	if user.TOTPEnabled {
		// MFA is already on, so there is no pending enrolment to confirm — and
		// therefore no path on which guessing codes could reach the secret of an
		// account that is already protected by it.
		return apperrors.NewConflict(domain.MsgMFAAlreadyEnabled)
	}

	allowed, spent := uc.consumeMFAAttempt(ctx, session.ID)
	if !allowed {
		uc.destroyEnrollment(ctx, session.ID, user.ID)
		return apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	secret, err := crypto.Decrypt(uc.settings.EncryptionKey, *user.TOTPSecretEnc)
	if err != nil {
		return err
	}
	if !totp.Validate(code, string(secret)) {
		if spent >= maxMFAAttempts {
			uc.destroyEnrollment(ctx, session.ID, user.ID)
		}
		return apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	// Enable, and clear what the confirmation spent, together: MFA is never on
	// for a session still carrying the debt of the attempts that turned it on.
	return uc.withinTx(ctx, func(txCtx context.Context) error {
		if err := uc.users.SetTOTP(txCtx, user.ID, user.TOTPSecretEnc, true); err != nil {
			return err
		}
		return uc.sessions.ClearMFAAttempts(txCtx, session.ID)
	})
}

// MfaVerify completes an mfa_pending session: it charges an attempt, validates
// the code, clears mfa_pending, and rotates the session token (fixation
// defense). Returns the new cookie token.
//
// The ORDER is the whole control (see the budget policy at the top of this
// file): the attempt is charged, atomically and durably, BEFORE the code is
// looked at, and every refusal answers in the same words. Once the budget is
// spent the pending session is destroyed, so the next guess has nothing left to
// guess against — the user starts again from /auth/login, with the password.
func (uc *UseCases) MfaVerify(ctx context.Context, sessionToken, code string) (*domain.LoginResult, error) {
	now := uc.now()

	session, err := uc.sessions.GetByTokenHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		return nil, err
	}
	if session == nil || !session.IsValid(now) || !session.MFAPending {
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	// CHARGE FIRST, VALIDATE SECOND — and charge before the user is even loaded,
	// so that every code submitted against a live pending session costs budget
	// whatever else turns out to be true of the account behind it.
	allowed, spent := uc.consumeMFAAttempt(ctx, session.ID)
	if !allowed {
		// The budget was already spent when this attempt arrived, which means an
		// earlier destroy did not stick (a caller that went away, a write that
		// failed). Destroying is idempotent, so do it again rather than leave a
		// pending session alive with nothing left protecting it.
		uc.destroyPendingSession(ctx, session.ID, session.UserID)
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	user, err := uc.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TOTPSecretEnc == nil {
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}
	secret, err := crypto.Decrypt(uc.settings.EncryptionKey, *user.TOTPSecretEnc)
	if err != nil {
		return nil, err
	}
	if !totp.Validate(code, string(secret)) {
		// The attempt that reached the threshold has just been spent on a wrong
		// code: the budget is gone, so the session goes with it.
		if spent >= maxMFAAttempts {
			uc.destroyPendingSession(ctx, session.ID, session.UserID)
		}
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidMFACode)
	}

	newToken, err := crypto.NewSessionToken()
	if err != nil {
		return nil, err
	}
	// CompleteMFA clears mfa_attempts in the same statement: a session is never
	// completed while still carrying the debt of the attempts that completed it.
	//
	// Unlike the login success path this deliberately runs on the CALLER's
	// context. If the client has gone there is nothing to preserve — rotating the
	// token for an answer nobody receives would strand a session whose new token
	// only the server knows, where leaving it pending costs that caller one
	// attempt of a budget it can still spend.
	if err := uc.sessions.CompleteMFA(ctx, session.ID, crypto.HashToken(newToken),
		now.Add(uc.settings.SessionIdleTTL), now.Add(uc.settings.SessionAbsoluteTTL)); err != nil {
		return nil, err
	}

	return &domain.LoginResult{SessionToken: newToken, MFARequired: false, User: user}, nil
}

// fullSession resolves a session cookie to its (session, user) for a route that
// carries no auth middleware, and enforces what RequireAuth enforces: a live
// session, an active user, and MFA already completed. Authenticate is reused
// rather than re-implemented so there stays exactly one definition of "this
// cookie is a live session".
func (uc *UseCases) fullSession(ctx context.Context, sessionToken string) (*domain.Session, *domain.User, error) {
	session, user, err := uc.Authenticate(ctx, sessionToken)
	if err != nil {
		return nil, nil, err
	}
	if session.MFAPending {
		return nil, nil, apperrors.NewUnauthorized(domain.MsgMFARequired)
	}
	return session, user, nil
}

// consumeMFAAttempt spends one attempt from the session's budget and reports
// whether this one was inside it, plus the count after the charge.
//
// It FAILS CLOSED, for the reason the login charge does: an attempt that could
// not be charged must not be admitted uncharged, or a database hiccup turns the
// budget off. The error is logged and swallowed — the caller answers the same
// refusal as every other failure, because surfacing a 500 from here would be its
// own oracle (a 500 for a live pending session while everything else answers 401
// identifies the session, and turns a wrong code into a way of getting a
// different answer).
func (uc *UseCases) consumeMFAAttempt(ctx context.Context, sessionID string) (bool, int) {
	allowed, spent, err := uc.sessions.ConsumeMFAAttempt(ctx, sessionID, maxMFAAttempts)
	if err != nil {
		logger.Log.WithContext(ctx).Error(
			"identity: MFA attempt could not be charged — refusing it rather than admitting it uncharged",
			logger.String("sessionId", sessionID), logger.Error(err))
		return false, 0
	}
	return allowed, spent
}

// destroyPendingSession revokes a pending session whose second-factor budget is
// spent. It runs on a context the client cannot cancel: a caller that hangs up
// after its last wrong code must not thereby save the session it was guessing
// against.
//
// The failure is logged, never returned. Every path into here is already
// answering the one refusal, and there is no answer that could say "the session
// survived" without saying it to the attacker too. This log line is the only
// server-side trace that the control fired — the response deliberately carries
// none.
func (uc *UseCases) destroyPendingSession(ctx context.Context, sessionID, userID string) {
	ctx = context.WithoutCancel(ctx)
	logger.Log.WithContext(ctx).Warn(
		"identity: pending session destroyed — its second-factor attempt budget is spent",
		logger.String("sessionId", sessionID), logger.String("userId", userID))

	if err := uc.sessions.Revoke(ctx, sessionID); err != nil {
		logger.Log.WithContext(ctx).Error(
			"identity: pending session could not be destroyed after its MFA budget was spent",
			logger.String("sessionId", sessionID), logger.Error(err))
	}
}

// destroyEnrollment discards a pending TOTP secret whose confirmation budget is
// spent and returns the session's budget, so the owner can enrol again at once —
// against a NEW secret, which is why resetting the counter concedes nothing: the
// guesses already made were against a secret that no longer exists.
//
// It only ever runs for a user with an UNCONFIRMED enrolment (MfaEnable refuses
// before the charge when TOTP is already on), so it can never become a way of
// turning a protected account's MFA off.
func (uc *UseCases) destroyEnrollment(ctx context.Context, sessionID, userID string) {
	ctx = context.WithoutCancel(ctx)
	logger.Log.WithContext(ctx).Warn(
		"identity: pending MFA enrolment discarded — its confirmation budget is spent",
		logger.String("sessionId", sessionID), logger.String("userId", userID))

	if err := uc.withinTx(ctx, func(txCtx context.Context) error {
		if err := uc.users.SetTOTP(txCtx, userID, nil, false); err != nil {
			return err
		}
		return uc.sessions.ClearMFAAttempts(txCtx, sessionID)
	}); err != nil {
		logger.Log.WithContext(ctx).Error(
			"identity: pending MFA enrolment could not be discarded after its budget was spent",
			logger.String("userId", userID), logger.Error(err))
	}
}
