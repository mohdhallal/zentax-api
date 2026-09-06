package usecases

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// THE FAILED-LOGIN LOCKOUT POLICY. This is the one place it is stated; STATUS.md
// repeats it for operators (and is the ONLY place timing figures are recorded —
// they are re-measured per change and go stale in comments), and the repository
// implements it in one statement.
//
// WHAT IT BOUNDS: five verifications of the STORED hash per account between
// successes, per fifteen minutes, INDEPENDENT OF CONCURRENCY. An attempt is
// charged to the budget BEFORE the password is verified, in one atomic
// statement, so a thousand simultaneous requests buy the same five verifications
// that a thousand serial ones do. "Between successes" is the exact bound: a
// success resets the counter (see below), so somebody who knows the password
// refills the budget every time they use it. That is the intended behaviour —
// the budget exists to bound GUESSING — but it is not an absolute cap on
// verifications per window, and a burst of correct passwords will mint more
// than five sessions.
//
//   - Five consecutive failed passwords lock the account for fifteen minutes.
//     The counter is per account, not per IP: nothing here throttles an attacker
//     spreading guesses across many accounts, nor MFA code guessing.
//   - A LIVE lock is never extended. While the window is running the counter
//     does not move and locked_until does not slide, so the requests being
//     refused cannot lengthen the refusal.
//   - An ELAPSED window CLEARS the debt. The next attempt after it starts a
//     fresh count at 1, so re-locking costs a full five attempts again, not one.
//     (Before this, the saturated counter left "attempts + 1 >= threshold" true
//     forever: one wrong password per window held a known address locked out
//     permanently, at four requests an hour.)
//   - EVERY attempt costs budget, the successful ones included — that is what
//     makes the bound hold under concurrency, since nothing can be known about
//     the password at the moment the charge is made. A success then clears the
//     counter and any lock, so serial logins never accumulate. The one visible
//     cost is that more than five SIMULTANEOUS logins by the same account get
//     the generic refusal for the surplus, even with the right password; they
//     succeed on retry, since the first success resets the budget.
//   - What that does NOT buy is denial-of-service resistance. An attacker who
//     knows an address can still hold it out indefinitely by paying a fresh
//     threshold of attempts per window (three windows for fifteen requests, from
//     the policy — not a measurement). Nor does anything here bound SPRAYING:
//     one guess each against ten thousand addresses never touches a budget. Only
//     per-principal rate limiting closes either (see "Not covered" in STATUS.md's
//     auth section); the lockout by itself makes the DoS cost five requests per
//     fifteen minutes rather than one.
//   - THE LOCK IS NEVER ANNOUNCED, to anybody. Once the budget is spent the
//     stored hash is not verified at all: the dummy is, and every caller — right
//     password or wrong — gets the same generic MsgInvalidCredentials. Verifying
//     the real hash while locked is what made the lock a PASSWORD ORACLE:
//     "locked" for the right password and "invalid credentials" for a wrong one
//     let whoever had just spent five requests locking an address read the
//     correct password straight off the responses, at full speed, while the
//     lock refused nothing. The cost of not leaking is that a locked-out
//     legitimate user is told only "invalid email or password" and must wait
//     out the window; the fix for that is a lockout notification e-mail or
//     self-service unlock, not a distinguishing response.
//   - Work is equalised, with one measured residual. Every path performs exactly
//     one argon2 verification, so neither the body nor the bulk of the work
//     identifies an address. The charge is the residual: it runs only for an
//     address that is registered AND active AND human AND has a password set —
//     locked or not, which is the improvement over charging only unlocked ones.
//     Unknown, disabled, invited-without-a-password and service addresses pay
//     none, so what the residual separates is "a live, password-holding account"
//     from everything else, not "registered" from "unknown". It is a few
//     milliseconds against a ~70 ms argon2 floor; the current measured figures
//     live in STATUS.md.
//   - NOT BOUNDED HERE: the argon2 verification itself is unmetered. Every
//     request pays 64 MiB and ~70 ms by design, including refusals for addresses
//     that do not exist, so a few hundred concurrent logins from one unauthorised
//     source will exhaust memory and stall the process long before any budget is
//     consulted. Bounding that needs a semaphore around the verification with a
//     uniform shed answer, alongside per-principal rate limiting; both are listed
//     under "Not covered" in STATUS.md's auth section.
const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

// Login verifies credentials and mints a session. If the user has TOTP enabled,
// the session is mfa_pending (unusable for tenant routes until MFA verify).
//
// CONNECTION DISCIPLINE. /auth/login deliberately declares no transaction
// (handlers.LoginHandler.DefineRoute); this use case owns its transactions and
// takes them SEQUENTIALLY, so the request holds at most one pooled connection at
// any instant: the lookup, then the consume statement, then ~70ms of argon2 with
// no connection held at all, then — only on success — one short transaction for
// the reset and the session insert. Running the consume inside a request
// transaction that stays open across the verification would hold a row lock for
// the duration of an argon2 hash and serialise the whole account behind it;
// running it on a second connection borrowed while the request holds the first
// deadlocked the API at PoolMax (measured, twice).
func (uc *UseCases) Login(ctx context.Context, input domain.LoginInput) (*domain.LoginResult, error) {
	// TrimSpace is the use case's own guarantee about the address, not a copy of
	// a rule enforced above it: the HTTP boundary happens to trim every body
	// string before validation (routing.sanitize), so `validate:"email"` never
	// sees padding and a padded address arrives here already canonical — but a
	// non-HTTP caller has no such layer. Both trims must exist for the same
	// address to mean the same account everywhere.
	email := strings.ToLower(strings.TrimSpace(input.Email))
	now := uc.now()

	user, err := uc.users.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	// loginCapable: this address could authenticate at all, ignoring the budget.
	// A service account is excluded on purpose — machines authenticate with API
	// tokens, never interactively — as are disabled members and invited ones who
	// have not set a password yet.
	loginCapable := user != nil && !user.IsService() && user.PasswordHash != nil && user.IsActive()

	// CHARGE FIRST, VERIFY SECOND. `allowed` says this attempt was inside the
	// budget, decided and committed by the same statement that spent it. The
	// alternative — deciding "locked" from the row read above and counting the
	// attempt afterwards — is what made the lockout throttle only SERIAL
	// guessing: every request that started before the threshold increment
	// committed read an unlocked account and verified the stored hash, so one
	// concurrent burst bought as many real verifications as the attacker had
	// threads. Reproduced deliberately and measured against the real router:
	// correct guesses released into a burst of forty wrong ones were logged
	// straight in (figures in STATUS.md); after this change, none are.
	allowed := false
	if loginCapable {
		allowed = uc.consumeAttempt(ctx, user.ID, now)
	}

	// WHICH hash is verified is the whole security decision here, and there are
	// exactly two mistakes available.
	//
	// Skipping the verification for a case that can be decided without it — as
	// the lock once did — forks the timing: a locked registered address answered
	// in 2.4ms where an unknown one paid 69ms, so a ~28x gap identified a
	// registered address for the price of the five requests that locked it.
	//
	// Verifying the STORED hash once the budget is spent — the other way round,
	// and worse — makes the outcome of that verification observable through the
	// answer, and the lock becomes a password oracle: lock an address, then guess
	// against it and read off which guess is right, unthrottled, because the lock
	// refuses no verification at all.
	//
	// So: exactly one argon2 verification on every path, against the stored hash
	// ONLY when the account is login-capable AND this attempt was inside the
	// budget, and against the fixed dummy in every other case — unknown address,
	// disabled member, service account, no password set, and every attempt past
	// the budget. The verification then decides nothing that the answer can
	// reveal except on the one path that succeeds.
	hash := dummyHash
	if loginCapable && allowed {
		hash = *user.PasswordHash
	}
	ok := uc.verifyOnce(ctx, input.Password, hash, user)

	// ONE refusal, byte-identical for every failure — not login-capable, out of
	// budget, or a verification that came back false. An account past its budget
	// is refused here without its stored hash ever having been touched, so
	// "locked" is not distinguishable from "wrong password" by the body, and the
	// correct password is not distinguishable from a wrong one either. Nothing
	// more is written: the attempt was already charged before the verification.
	// That is also what makes aborting useless — a client that hangs up after the
	// charge cannot un-spend it, and one that hangs up before it breaks the
	// charge, which refuses rather than admits (consumeAttempt fails closed).
	// The price is real and deliberate: a locked-out owner is told only "invalid
	// email or password" and has to wait out the window (see the policy above).
	if !loginCapable || !allowed || !ok {
		return nil, apperrors.NewUnauthorized(domain.MsgInvalidCredentials)
	}

	// Success clears the attempt just consumed, the count before it, AND any
	// lock left over from an earlier lockout whose window has since elapsed — a
	// stale locked_until must not outlive the lockout it recorded. The reset is
	// unconditional now: this login spent an attempt to get here, so the counter
	// is never already zero. It shares one short transaction with the session
	// insert, so a session never exists for a login whose counter was not
	// cleared. Racing a concurrent failed attempt is harmless: the consume
	// statement and this reset each write the counter and the lock together, so
	// whichever lands last leaves a consistent pair.
	// The transaction runs on a context the client cannot cancel. This login has
	// already spent an attempt, and if it was the attempt that reached the
	// threshold the lock is already stamped; a client that hangs up between the
	// charge and the reset would otherwise leave the account locked for the full
	// window despite having presented the correct password. A session row minted
	// for a caller that has gone is the pre-existing, harmless case of a commit
	// whose response is lost.
	var token string
	if err := uc.withinTx(context.WithoutCancel(ctx), func(txCtx context.Context) error {
		if err := uc.users.ResetFailedLogin(txCtx, user.ID); err != nil {
			return err
		}
		t, _, err := uc.createSession(txCtx, user, user.TOTPEnabled, input.IP, input.UserAgent)
		if err != nil {
			return err
		}
		token = t
		return nil
	}); err != nil {
		return nil, err
	}

	return &domain.LoginResult{
		SessionToken: token,
		MFARequired:  user.TOTPEnabled,
		User:         user,
	}, nil
}

// consumeAttempt spends one attempt from the account's budget and reports
// whether this one was inside it, handing the repository the policy above
// (threshold, window, and "now") to apply under the row lock.
//
// It FAILS CLOSED. A consume that errors means the attempt could not be charged,
// and admitting an uncharged attempt is precisely the bypass this design exists
// to remove — a database hiccup must not turn the budget off. Refusing costs a
// legitimate user one login they can retry; the alternative costs the account.
//
// The error is logged and swallowed rather than returned, and the caller answers
// the same generic 401 as every other refusal. Surfacing it would be an oracle
// from the other side: this statement runs ONLY for a registered, login-capable
// address, so a 500 here — while an unknown address kept answering 401 — would
// identify the address, and would hand an attacker a way to turn a rejected
// password into a server error.
func (uc *UseCases) consumeAttempt(ctx context.Context, userID string, now time.Time) bool {
	allowed, err := uc.users.ConsumeLoginAttempt(ctx, userID, maxFailedAttempts, now, now.Add(lockoutDuration))
	if err != nil {
		logger.Log.WithContext(ctx).Error(
			"identity: login attempt could not be charged — refusing it rather than admitting it uncharged",
			logger.String("userId", userID), logger.Error(err))
		return false
	}
	if !allowed {
		// The only server-side trace that a lockout is in force. The response is
		// deliberately indistinguishable from every other refusal, so without
		// this line an operator investigating "I cannot sign in" — or an auditor
		// asking whether the control ever fires — has nothing to look at. It
		// leaks nothing: it is written to the log, never to the caller.
		logger.Log.WithContext(ctx).Warn(
			"identity: login refused — the account's attempt budget is spent and the lockout window is running",
			logger.String("userId", userID))
	}
	return allowed
}

// verifyOnce performs the single argon2 verification every login path owes, and
// keeps "equal work" STRUCTURAL rather than incidental.
//
// A hash this process cannot decode is a broken stored credential, not a
// different KIND of answer: returning 500 here while every other refusal is 401
// would re-open the oracle from the other side. But answering 401 was not
// enough on its own — crypto.VerifyPassword decodes before it hashes, so an
// undecodable stored hash returned in ~2.4ms against everyone else's ~70ms
// floor. A corrupt or legacy-format row was therefore identifiable by timing
// alone. So the decode failure falls back to verifying the dummy, which pays
// exactly the cost every other refusal pays; the first call did no argon2 work
// at all, so this is still one hashing per request.
func (uc *UseCases) verifyOnce(ctx context.Context, password, hash string, user *domain.User) bool {
	ok, err := uc.verifyPassword(password, hash)
	if err == nil {
		return ok
	}

	fields := []logger.Field{logger.Error(err)}
	if user != nil {
		fields = append(fields, logger.String("userId", user.ID))
	}
	logger.Log.WithContext(ctx).Error("identity: password hash could not be verified", fields...)

	if hash != dummyHash {
		if _, dummyErr := uc.verifyPassword(password, dummyHash); dummyErr != nil {
			// The equalizer itself is broken (it is built at load from
			// crypto.HashPassword). Nothing is left to pay the cost with; the
			// answer is still the generic 401.
			logger.Log.WithContext(ctx).Error(
				"identity: timing-equalizer hash could not be verified — refusals are no longer equal-cost",
				logger.Error(dummyErr))
		}
	}
	return false
}
